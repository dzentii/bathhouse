package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	DB     *pgxpool.Pool
	Config platform.Config
	Redis  *redis.Client
}
type OrderContext struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Amount    int64      `json:"amount_kopecks"`
	Currency  string     `json:"currency"`
	PriceHash string     `json:"price_hash"`
	ExpiresAt *time.Time `json:"expires_at"`
}
type Attempt struct {
	ID          string    `json:"id"`
	OrderID     string    `json:"order_id"`
	Amount      int64     `json:"amount_kopecks"`
	Currency    string    `json:"currency"`
	Outcome     string    `json:"outcome"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	RequestHash string    `json:"-"`
}

const columns = "id::text,order_id::text,amount_kopecks,currency,outcome,status,reason,created_at,updated_at,request_hash"

func scan(row pgx.Row) (Attempt, error) {
	var a Attempt
	e := row.Scan(&a.ID, &a.OrderID, &a.Amount, &a.Currency, &a.Outcome, &a.Status, &a.Reason, &a.CreatedAt, &a.UpdatedAt, &a.RequestHash)
	return a, e
}
func (s *Service) order(r *http.Request) (OrderContext, error) {
	var o OrderContext
	id := chi.URLParam(r, "orderID")
	parsed, parseErr := uuid.Parse(id)
	if parseErr != nil {
		return o, platform.Err(404, "not_found", "Заказ не найден")
	}
	id = parsed.String()
	// #nosec G704 -- The URL base is trusted startup configuration; only a canonical UUID is appended.
	req, e := http.NewRequestWithContext(r.Context(), http.MethodGet, s.Config.BookingURL+"/internal/orders/"+id+"/payment-context", nil)
	if e != nil {
		return o, e
	}
	req.Header.Set("X-Internal-Key", s.Config.InternalKey)
	req.Header.Set("X-Order-Token", platform.Bearer(r))
	// #nosec G704 -- Same validated, fixed-host internal request; redirects are disabled.
	res, e := platform.Client.Do(req)
	if e != nil {
		return o, platform.Err(503, "booking_unavailable", "Сервис бронирования недоступен")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		if res.StatusCode == 404 {
			return o, platform.Err(404, "not_found", "Заказ не найден")
		}
		return o, platform.Err(503, "booking_unavailable", "Не удалось проверить заказ")
	}
	e = json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&o)
	return o, e
}
func (s *Service) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(platform.Internal(s.Config.InternalKey))
		r.Post("/internal/order-payment-summaries", s.summaries)
		r.Post("/internal/payment-history", s.internalHistory)
	})
	r.With(platform.RequireIdempotency(s.Config.InternalKey)).Post("/api/v1/orders/{orderID}/payments", s.create)
	r.Get("/api/v1/orders/{orderID}/payments/{paymentID}", s.read)
	r.Get("/api/v1/orders/{orderID}/payments", func(w http.ResponseWriter, r *http.Request) { s.listAttempts(w, r, false) })
	r.With(platform.AdminOnly(s.Config)).Get("/api/v1/admin/orders/{orderID}/payments", func(w http.ResponseWriter, r *http.Request) { s.listAttempts(w, r, true) })

}
func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Amount    int64  `json:"amount_kopecks" validate:"required,gt=0"`
		Currency  string `json:"currency" validate:"required,eq=RUB"`
		PriceHash string `json:"price_hash" validate:"required,len=64"`
		Outcome   string `json:"outcome" validate:"required,oneof=success declined"`
	}
	if e := platform.Decode(w, r, &in); e != nil {
		platform.Fail(w, e)
		return
	}
	key, e := platform.IdempotencyKey(r)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	o, e := s.order(r)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	body, _ := json.Marshal(in)
	hash := platform.KeyedHash(s.Config.InternalKey, string(body))
	key = platform.Hash(key)
	previous, e := scan(s.DB.QueryRow(r.Context(), "SELECT "+columns+" FROM attempts WHERE order_id=$1 AND idempotency_hash=$2", o.ID, key))
	if e == nil {
		if previous.RequestHash != hash {
			platform.Fail(w, platform.Conflict("idempotency_conflict", "Ключ уже использован с другим запросом"))
			return
		}
		platform.JSON(w, 200, previous)
		return
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		platform.Fail(w, e)
		return
	}
	if o.Status != "held" {
		platform.Fail(w, platform.Conflict("order_inactive", "Для оплаты нужно действующее удержание"))
		return
	}
	if in.Amount != o.Amount || in.Currency != o.Currency || in.PriceHash != o.PriceHash {
		platform.Fail(w, platform.Conflict("price_confirmation_required", "Подтвердите актуальную полную стоимость и price_hash удержанного заказа"))
		return
	}
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	id := uuid.NewString()
	result, e := tx.Exec(r.Context(), "INSERT INTO attempts(id,order_id,idempotency_hash,request_hash,amount_kopecks,currency,outcome,status) VALUES($1,$2,$3,$4,$5,$6,$7,'pending') ON CONFLICT(order_id,idempotency_hash) DO NOTHING", id, o.ID, key, hash, in.Amount, in.Currency, in.Outcome)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	status := 202
	var a Attempt
	if result.RowsAffected() == 1 {
		e = platform.Emit(r.Context(), tx, "payment.result", o.ID, platform.PaymentResult{PaymentID: id, OrderID: o.ID, Amount: in.Amount, Currency: in.Currency, Outcome: in.Outcome})
		if e == nil {
			a, e = scan(tx.QueryRow(r.Context(), "SELECT "+columns+" FROM attempts WHERE id=$1", id))
		}
	} else {
		a, e = scan(tx.QueryRow(r.Context(), "SELECT "+columns+" FROM attempts WHERE order_id=$1 AND idempotency_hash=$2", o.ID, key))
		status = 200
		if e == nil && a.RequestHash != hash {
			e = platform.Conflict("idempotency_conflict", "Ключ уже использован с другим запросом")
		}
	}
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, status, a)
}
func (s *Service) read(w http.ResponseWriter, r *http.Request) {
	o, e := s.order(r)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	id := chi.URLParam(r, "paymentID")
	if uuid.Validate(id) != nil {
		platform.Fail(w, platform.Err(404, "not_found", "Попытка не найдена"))
		return
	}
	a, e := scan(s.DB.QueryRow(r.Context(), "SELECT "+columns+" FROM attempts WHERE id=$1 AND order_id=$2", id, o.ID))
	if errors.Is(e, pgx.ErrNoRows) {
		platform.Fail(w, platform.Err(404, "not_found", "Попытка не найдена"))
		return
	}
	if e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, a)
}
func (s *Service) HandleEvent(ctx context.Context, e platform.Event) error {
	if e.Type != "booking.payment_processed" {
		return nil
	}
	var d platform.PaymentProcessed
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return err
	}
	if uuid.Validate(d.PaymentID) != nil || uuid.Validate(d.OrderID) != nil || (d.Status != "accepted" && d.Status != "declined" && d.Status != "rejected") {
		return errors.New("invalid payment decision")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fresh, err := platform.Inbox(ctx, tx, e.ID)
	if err != nil {
		return err
	}
	if !fresh {
		return tx.Commit(ctx)
	}
	res, err := tx.Exec(ctx, "UPDATE attempts SET status=$2,reason=$3,updated_at=clock_timestamp() WHERE id=$1 AND order_id=$4 AND status='pending'", d.PaymentID, d.Status, d.Reason, d.OrderID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		var exists bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM attempts WHERE id=$1 AND order_id=$2)", d.PaymentID, d.OrderID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errors.New("payment attempt not found")
		}
	}
	return tx.Commit(ctx)
}
func Start(ctx context.Context) error {
	c, e := platform.Load("payment")
	if e != nil {
		return e
	}
	db, e := platform.DB(ctx, c.DatabaseURL)
	if e != nil {
		return fmt.Errorf("database unavailable")
	}
	defer db.Close()
	rc, e := platform.Redis(c.RedisURL)
	if e != nil {
		return e
	}
	defer func() { _ = rc.Close() }()
	s := &Service{DB: db, Config: c, Redis: rc}
	work, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }()
	wg.Add(3)
	go func() { defer wg.Done(); platform.RunPublisher(work, db, c) }()
	go func() { defer wg.Done(); platform.RunConsumer(work, c, "payment.v1", s.HandleEvent) }()
	go func() { defer wg.Done(); platform.RunDeliveryCleanup(work, db, c) }()
	r := platform.Router(c, db, rc)
	s.Routes(r)
	return platform.Serve(ctx, c.Addr, r)
}
