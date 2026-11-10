package booking

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/jackc/pgx/v5"
)

type PaymentAttempt struct {
	ID        string    `json:"id"`
	OrderID   string    `json:"order_id"`
	Amount    int64     `json:"amount_kopecks"`
	Currency  string    `json:"currency"`
	Outcome   string    `json:"outcome"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type PaymentSummary struct {
	Status      string          `json:"status"`
	Paid        int64           `json:"paid_kopecks"`
	Currency    string          `json:"currency"`
	TestMode    bool            `json:"test_mode"`
	LastAttempt *PaymentAttempt `json:"last_attempt,omitempty"`
}

func (s *Service) paymentSummaries(r *http.Request, orders []Order) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]string, len(orders))
	for i := range orders {
		ids[i] = orders[i].ID
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.Config.PaymentURL+"/internal/order-payment-summaries", bytes.NewReader(bytesJSON(map[string]any{"order_ids": ids})))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", s.Config.InternalKey)
	res, err := platform.Client.Do(req)
	if err != nil {
		return platform.Err(503, "payment_unavailable", "Платёжная сводка временно недоступна; повторите запрос")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return platform.Err(503, "payment_unavailable", "Не удалось получить фактический результат оплаты")
	}
	var response struct {
		Items map[string]PaymentAttempt `json:"items"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&response); err != nil {
		return platform.Err(503, "payment_unavailable", "Платёжная сводка временно недоступна; повторите запрос")
	}
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	rows, err := tx.Query(r.Context(), "SELECT "+orderColumns+" FROM orders WHERE id=ANY($1::uuid[])", ids)
	if err != nil {
		return err
	}
	currentByID := make(map[string]Order, len(ids))
	for rows.Next() {
		current, scanErr := scanOrder(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		currentByID[current.ID] = current
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type decision struct {
		status, reason string
		at             time.Time
	}
	decisions := map[string]decision{}
	paymentIDs := []string{}
	for _, a := range response.Items {
		paymentIDs = append(paymentIDs, a.ID)
	}
	if len(paymentIDs) > 0 {
		rows, err = tx.Query(r.Context(), "SELECT payment_id::text,order_id::text,status,reason,created_at FROM payment_decisions WHERE payment_id=ANY($1::uuid[]) AND order_id=ANY($2::uuid[])", paymentIDs, ids)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, orderID string
			var d decision
			if err = rows.Scan(&id, &orderID, &d.status, &d.reason, &d.at); err != nil {
				rows.Close()
				return err
			}
			decisions[orderID+":"+id] = d
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for i, original := range orders {
		id := original.ID
		current, ok := currentByID[id]
		if !ok {
			return platform.Err(404, "not_found", "Заказ не найден")
		}
		if current.Status == "held" && current.ExpiresAt != nil && !current.ServerTime.Before(*current.ExpiresAt) {
			current.Status = "expired"
		}
		s.decorate(&current)
		orders[i] = current
		o := &orders[i]
		summary := &PaymentSummary{Status: "none", Currency: "RUB", TestMode: true}
		if attempt, ok := response.Items[o.ID]; ok {
			if d, found := decisions[o.ID+":"+attempt.ID]; found {
				attempt.Status = d.status
				attempt.Reason = d.reason
				attempt.UpdatedAt = d.at
			}
			summary.Status = attempt.Status
			summary.LastAttempt = &attempt
		}
		if o.Status == "confirmed" {
			summary.Status = "accepted"
			summary.Paid = o.Amount
		}
		o.Payment = summary
	}
	return tx.Commit(r.Context())
}
