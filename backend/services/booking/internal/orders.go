package booking

import (
	"errors"
	"net/http"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) quote(w http.ResponseWriter, r *http.Request) {
	var sel Selection
	if e := platform.Decode(w, r, &sel); e != nil {
		platform.Fail(w, e)
		return
	}
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	op, e := platform.LoadOperation(r, tx, "quote", s.Config.InternalKey)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if op.Replay(w, r) {
		return
	}
	d, e := department(r.Context(), tx, sel.DepartmentID, false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	now, e := nowDB(r.Context(), tx)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	q, e := buildQuote(r.Context(), tx, d, sel, now)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	op.Commit(w, r, 200, q)
}
func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Selection Selection `json:"selection" validate:"required"`
		Buyer     Buyer     `json:"buyer" validate:"required"`
	}
	if e := platform.Decode(w, r, &in); e != nil {
		platform.Fail(w, e)
		return
	}
	key, e := idempotency(r)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	in.Buyer, e = normalizeBuyer(in.Buyer)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	in.Selection.StartsAt = in.Selection.StartsAt.UTC()
	in.Selection.EndsAt = in.Selection.EndsAt.UTC()
	hash := platform.Hash(string(bytesJSON(in)))
	// A create key is a 128-bit random capability for recovering the original response.
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	// The random create key is the sole recovery capability until its access expires.
	existing, err := scanOrder(tx.QueryRow(r.Context(), "SELECT "+orderColumns+" FROM orders WHERE idempotency_hash=$1", key))
	if err == nil && !existing.ServerTime.Before(existing.AccessExpiresAt) {
		platform.Fail(w, platform.Err(404, "not_found", "Заказ не найден"))
		return
	}
	var ae *platform.APIError
	if err != nil && (!errors.As(err, &ae) || ae.Status != 404) {
		platform.Fail(w, err)
		return
	}
	op, e := platform.LoadOperation(r, tx, "order:create", s.Config.InternalKey)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	// The receipt lock can wait behind another request; recheck expiry afterwards.
	if err == nil {
		existing, e = getOrder(r.Context(), tx, existing.ID, true)
		if e != nil {
			platform.Fail(w, e)
			return
		}
		if !existing.ServerTime.Before(existing.AccessExpiresAt) {
			platform.Fail(w, platform.Err(404, "not_found", "Заказ не найден"))
			return
		}
	}
	if op.Replay(w, r) {
		return
	}
	if err == nil {
		if existing.RequestHash != hash {
			platform.Fail(w, platform.Conflict("idempotency_conflict", "Ключ уже использован с другим запросом"))
			return
		}
		s.decorate(&existing)
		op.BindOrder(existing.ID, existing.AccessExpiresAt)
		op.Commit(w, r, 200, map[string]any{"order": existing, "order_token": capability(existing.ID, s.Config.OrderKey, "order")})
		return
	}
	d, e := department(r.Context(), tx, in.Selection.DepartmentID, true)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	now, e := nowDB(r.Context(), tx)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if e = requireSales(d, now); e != nil {
		platform.Fail(w, e)
		return
	}
	q, e := buildQuote(r.Context(), tx, d, in.Selection, now)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	id := uuid.NewString()
	token := capability(id, s.Config.OrderKey, "order")
	res, e := tx.Exec(r.Context(), `INSERT INTO orders(id,department_id,status,access_hash,idempotency_hash,request_hash,selection,buyer,quote,amount_kopecks,starts_at,ends_at) VALUES($1,$2,'draft',$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(idempotency_hash) DO NOTHING`, id, d.ID, platform.Hash(token), key, hash, bytesJSON(in.Selection), bytesJSON(in.Buyer), bytesJSON(q), q.Amount, in.Selection.StartsAt, in.Selection.EndsAt)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	status := 201
	var o Order
	if res.RowsAffected() == 0 {
		o, e = scanOrder(tx.QueryRow(r.Context(), "SELECT "+orderColumns+" FROM orders WHERE idempotency_hash=$1", key))
		status = 200
		if e == nil && o.RequestHash != hash {
			e = platform.Conflict("idempotency_conflict", "Ключ уже использован с другим запросом")
		}
	} else {
		if _, e = tx.Exec(r.Context(), "INSERT INTO order_history(order_id,action,details) VALUES($1,'order.created',$2)", id, bytesJSON(map[string]any{"department_id": d.ID, "amount_kopecks": q.Amount})); e != nil {
			platform.Fail(w, e)
			return
		}
		o, e = getOrder(r.Context(), tx, id, false)
	}
	if e != nil {
		platform.Fail(w, e)
		return
	}
	s.decorate(&o)
	op.BindOrder(o.ID, o.AccessExpiresAt)
	op.Commit(w, r, status, map[string]any{"order": o, "order_token": capability(o.ID, s.Config.OrderKey, "order")})
}
func (s *Service) read(w http.ResponseWriter, r *http.Request) {
	o, e := s.readOrder(r, false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, o)
}
func (s *Service) readOrder(r *http.Request, admin bool) (Order, error) {
	tx, _, o, e := s.lockedOrder(r.Context(), chi.URLParam(r, "orderID"), platform.Bearer(r), admin)
	if e != nil {
		return o, e
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if e = tx.Commit(r.Context()); e != nil {
		return o, e
	}
	s.decorate(&o)
	return o, nil
}
func (s *Service) hold(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if e := platform.Decode(w, r, &input); e != nil {
		platform.Fail(w, e)
		return
	}
	tx, d, o, e := s.lockedOrder(r.Context(), chi.URLParam(r, "orderID"), platform.Bearer(r), false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	op, e := platform.LoadOperation(r, tx, "order:"+o.ID+":"+platform.Hash(platform.Bearer(r)), s.Config.InternalKey)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if op.Replay(w, r) {
		return
	}
	if o.Status == "held" || o.Status == "confirmed" {
		s.decorate(&o)
		op.BindOrder(o.ID, o.AccessExpiresAt)
		op.Commit(w, r, 200, map[string]any{"order": o, "price_changed": false})
		return
	}
	if o.Status != "draft" {
		if e = tx.Commit(r.Context()); e != nil {
			platform.Fail(w, e)
			return
		}
		platform.Fail(w, platform.Conflict("order_inactive", "Нужно создать новый заказ"))
		return
	}
	oldAmount := o.Amount
	now, e := nowDB(r.Context(), tx)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if now.Add(10 * time.Minute).After(o.AccessExpiresAt) {
		platform.Fail(w, platform.Conflict("order_access_expiring", "Срок оформления заканчивается; создайте новый заказ для полного удержания на 10 минут"))
		return
	}
	if e = requireSales(d, now); e != nil {
		platform.Fail(w, e)
		return
	}
	q, e := buildQuote(r.Context(), tx, d, o.Selection, now)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	states, e := availability(r.Context(), tx, d, o.Selection.StartsAt, spanEnd(d, o.Selection))
	if e != nil {
		platform.Fail(w, e)
		return
	}
	available := map[string]bool{}
	for _, x := range states {
		available[x.ID] = x.State == "free"
	}
	for _, id := range o.Selection.ResourceIDs {
		if !available[id] {
			platform.Fail(w, platform.Conflict("resource_unavailable", "Часть выбора недоступна; ни один ресурс не удержан"))
			return
		}
	}
	clientKey, e := s.reserveClientQuota(r, tx, len(o.Selection.ResourceIDs))
	if e != nil {
		w.Header().Set("Retry-After", "60")
		platform.Fail(w, e)
		return
	}
	// Reserve late in the transaction so the full ten-minute period starts after validation.
	q.Preliminary = false
	var held, expires time.Time
	e = tx.QueryRow(r.Context(), "WITH timing AS MATERIALIZED (SELECT clock_timestamp() AS t) UPDATE orders SET version=version+1,status='held',hold_client_key=$4,held_at=timing.t,expires_at=timing.t+interval '10 minutes',quote=$2,amount_kopecks=$3 FROM timing WHERE id=$1 AND access_expires_at>=timing.t+interval '10 minutes' RETURNING held_at,expires_at", o.ID, bytesJSON(q), q.Amount, clientKey).Scan(&held, &expires)
	if errors.Is(e, pgx.ErrNoRows) {
		e = platform.Conflict("order_access_expiring", "Срок оформления заканчивается; создайте новый заказ")
	}
	if e != nil {
		platform.Fail(w, e)
		return
	}
	for _, id := range o.Selection.ResourceIDs {
		if _, e = tx.Exec(r.Context(), "INSERT INTO allocations(order_id,resource_id,span) VALUES($1,$2,tstzrange($3,$4,'[)'))", o.ID, id, o.Selection.StartsAt, spanEnd(d, o.Selection)); e != nil {
			platform.Fail(w, e)
			return
		}
	}
	if e = emitOrder(r.Context(), tx, "order.held", o.ID, map[string]any{"order_id": o.ID, "expires_at": expires}); e != nil {
		platform.Fail(w, e)
		return
	}
	o, e = getOrder(r.Context(), tx, o.ID, false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	op.BindOrder(o.ID, o.AccessExpiresAt)
	op.Commit(w, r, 200, map[string]any{"order": o, "price_changed": oldAmount != q.Amount})
}
func (s *Service) release(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if e := platform.Decode(w, r, &in); e != nil {
		platform.Fail(w, e)
		return
	}
	tx, _, o, e := s.lockedOrder(r.Context(), chi.URLParam(r, "orderID"), platform.Bearer(r), false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	op, e := platform.LoadOperation(r, tx, "order:"+o.ID+":"+platform.Hash(platform.Bearer(r)), s.Config.InternalKey)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if op.Replay(w, r) {
		return
	}
	if o.Status == "confirmed" {
		platform.Fail(w, platform.Conflict("already_confirmed", "Оплаченный тестовый заказ нельзя освободить"))
		return
	}
	if o.Status == "held" || o.Status == "draft" {
		if _, e = tx.Exec(r.Context(), "UPDATE orders SET version=version+1,status='released' WHERE id=$1", o.ID); e != nil {
			platform.Fail(w, e)
			return
		}
		if _, e = tx.Exec(r.Context(), "UPDATE allocations SET active=false WHERE order_id=$1", o.ID); e != nil {
			platform.Fail(w, e)
			return
		}
		if e = emitOrder(r.Context(), tx, "order.released", o.ID, map[string]string{"order_id": o.ID}); e != nil {
			platform.Fail(w, e)
			return
		}
	}
	o, e = getOrder(r.Context(), tx, o.ID, false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	op.BindOrder(o.ID, o.AccessExpiresAt)
	op.Commit(w, r, 200, o)
}
func (s *Service) paymentContext(w http.ResponseWriter, r *http.Request) {
	tx, _, o, e := s.lockedOrder(r.Context(), chi.URLParam(r, "orderID"), r.Header.Get("X-Order-Token"), false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if e = tx.Commit(r.Context()); e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, map[string]any{"id": o.ID, "status": o.Status, "amount_kopecks": o.Amount, "currency": "RUB", "price_hash": o.Quote.PriceHash, "expires_at": o.ExpiresAt})
}
func (s *Service) ticket(w http.ResponseWriter, r *http.Request) {
	ticket, err := s.loadTicket(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, ticket)
}
