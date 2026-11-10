package booking

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func (s *Service) orderHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "orderID")
	if _, err := getOrder(r.Context(), s.DB, id, false); err != nil {
		platform.Fail(w, err)
		return
	}
	window, err := platform.HistoryQuery(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,action,details,actor_id,created_at FROM (
 SELECT CASE WHEN source_event_id IS NULL THEN 'booking:'||id::text ELSE 'event:'||source_event_id::text END AS id,action,details,COALESCE(actor_id::text,'') AS actor_id,created_at FROM order_history WHERE order_id=$1
 ) history WHERE (created_at,id COLLATE "C")<($2,$3) ORDER BY created_at DESC,id COLLATE "C" DESC LIMIT $4`, id, window.Before, window.BeforeID, window.Limit)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	items := []platform.HistoryEntry{}
	for rows.Next() {
		var e platform.HistoryEntry
		e.Source = "booking"
		e.EntityID = id
		if err = rows.Scan(&e.ID, &e.Action, &e.Details, &e.ActorID, &e.CreatedAt); err != nil {
			rows.Close()
			platform.Fail(w, err)
			return
		}
		items = append(items, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var attempts struct {
		Items []PaymentAttempt `json:"items"`
	}
	input := map[string]any{"order_id": id, "limit": window.Limit, "before": window.Before, "before_id": window.BeforeID}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.Config.PaymentURL+"/internal/payment-history", bytes.NewReader(bytesJSON(input)))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = internalResponse(r.Context(), req, s.Config.InternalKey, &attempts); err != nil {
		platform.Fail(w, err)
		return
	}
	for _, a := range attempts.Items {
		items = append(items, platform.HistoryEntry{ID: "payment:" + a.ID, Source: "payment", Action: "payment.created", EntityID: id, CreatedAt: a.CreatedAt, Details: bytesJSON(map[string]any{"payment_id": a.ID, "amount_kopecks": a.Amount, "currency": a.Currency, "outcome": a.Outcome})})
	}
	platform.HistoryResponse(w, items, window.Limit)
}
func (s *Service) departmentHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "departmentID")
	if len(id) < 1 || len(id) > 80 {
		platform.Fail(w, platform.Bad("Некорректное отделение"))
		return
	}
	window, err := platform.HistoryQuery(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	query := url.Values{"limit": {strconv.Itoa(window.Limit)}, "before": {window.Before.Format(time.RFC3339Nano)}, "before_id": {window.BeforeID}}
	// #nosec G704 -- Fixed configured service origin; the department ID is escaped as one path segment and query values are encoded.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.Config.CatalogURL+"/internal/departments/"+url.PathEscape(id)+"/history?"+query.Encode(), nil)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var response struct {
		Items []platform.HistoryEntry `json:"items"`
	}
	if err = internalResponse(r.Context(), req, s.Config.InternalKey, &response); err != nil {
		platform.Fail(w, err)
		return
	}
	items := response.Items
	rows, err := s.DB.Query(r.Context(), `SELECT id,action,entity_id,admin_id,before_value,after_value,created_at FROM (
 SELECT 'booking-audit:'||id::text AS id,action,entity_id,admin_id::text,before_value,after_value,created_at FROM audit
 WHERE entity_id=$1 OR entity_id IN(SELECT id::text FROM resource_blocks WHERE department_id=$1)
 ) history WHERE (created_at,id COLLATE "C")<($2,$3) ORDER BY created_at DESC,id COLLATE "C" DESC LIMIT $4`, id, window.Before, window.BeforeID, window.Limit)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var e platform.HistoryEntry
		e.Source = "booking"
		if err = rows.Scan(&e.ID, &e.Action, &e.EntityID, &e.ActorID, &e.Before, &e.After, &e.CreatedAt); err != nil {
			platform.Fail(w, err)
			return
		}
		items = append(items, e)
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.HistoryResponse(w, items, window.Limit)
}
func internalResponse(ctx context.Context, req *http.Request, key string, target any) error {
	req.Header.Set("X-Internal-Key", key)
	req.Header.Set("Content-Type", "application/json")
	// #nosec G704 -- Callers use validated configured service origins and escaped path segments; Client forbids redirects.
	res, err := platform.Client.Do(req)
	if err != nil {
		return platform.Err(503, "service_unavailable", "Сервис истории временно недоступен")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == 404 {
		return platform.Err(404, "not_found", "Объект не найден")
	}
	if res.StatusCode != 200 {
		return platform.Err(503, "service_unavailable", "Не удалось получить полную историю")
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(target); err != nil {
		return platform.Err(503, "service_unavailable", "Не удалось прочитать полную историю")
	}
	return ctx.Err()
}
