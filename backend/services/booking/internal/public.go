package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func (s *Service) Routes(r chi.Router) {
	r.With(platform.RequireIdempotency(s.Config.InternalKey)).Post("/api/v1/quotes", s.quote)
	r.With(platform.RequireIdempotency(s.Config.InternalKey)).Post("/api/v1/orders", s.create)
	r.Get("/api/v1/orders/{orderID}", s.read)
	r.With(platform.RequireIdempotency(s.Config.InternalKey)).Patch("/api/v1/orders/{orderID}", s.patchOrder)
	r.With(platform.RequireIdempotency(s.Config.InternalKey)).Post("/api/v1/orders/{orderID}/hold", s.hold)
	r.With(platform.RequireIdempotency(s.Config.InternalKey)).Post("/api/v1/orders/{orderID}/release", s.release)
	r.Get("/api/v1/tickets/{token}", s.ticket)
	r.Get("/api/v1/tickets/{token}/pdf", s.downloadTicket)
	r.Get("/api/v1/departments/{departmentID}/booking-config", s.config)
	r.Get("/api/v1/departments/{departmentID}/resources", s.resourceList)
	r.Get("/api/v1/departments/{departmentID}/availability", s.available)
	r.Get("/api/v1/departments/{departmentID}/availability-calendar", s.calendar)
	r.Group(func(r chi.Router) {
		r.Use(platform.Internal(s.Config.InternalKey))
		r.Get("/internal/orders/{orderID}/payment-context", s.paymentContext)
		r.Post("/internal/catalog/validate-department", s.validateCatalog)
	})
	r.Group(func(r chi.Router) {
		r.Use(platform.AdminOnly(s.Config), platform.RequireIdempotency(s.Config.InternalKey))
		r.Get("/api/v1/admin/orders", s.adminOrders)
		r.Post("/api/v1/admin/orders/search", s.searchOrders)
		r.Get("/api/v1/admin/departments/{departmentID}/occupancy", s.occupancy)
		r.Get("/api/v1/admin/orders/{orderID}", func(w http.ResponseWriter, r *http.Request) {
			o, e := s.readOrder(r, true)
			if e != nil {
				platform.Fail(w, e)
				return
			}
			items := []Order{o}
			if e = s.paymentSummaries(r, items); e != nil {
				platform.Fail(w, e)
				return
			}
			platform.JSON(w, 200, items[0])
		})
		r.Get("/api/v1/admin/schedules/{departmentID}", s.adminConfig)
		r.Put("/api/v1/admin/schedules/{departmentID}", s.saveSchedule)
		r.Get("/api/v1/admin/tariffs/{departmentID}", s.adminConfig)
		r.Put("/api/v1/admin/tariffs/{departmentID}", s.saveTariffs)
		r.Get("/api/v1/admin/resource-blocks", s.listBlocks)
		r.Post("/api/v1/admin/resource-blocks", s.block)
		r.Delete("/api/v1/admin/resource-blocks/{blockID}", s.unblock)
		r.Get("/api/v1/admin/audit", s.auditList)
		r.Get("/api/v1/admin/orders/{orderID}/history", s.orderHistory)
		r.Get("/api/v1/admin/departments/{departmentID}/history", s.departmentHistory)
		r.Get("/api/v1/admin/departments/{departmentID}/booking-policy", s.getPolicy)
		r.Patch("/api/v1/admin/departments/{departmentID}/booking-policy", s.patchPolicy)
		r.Get("/api/v1/admin/departments/{departmentID}/resources", s.adminResources)
		r.Post("/api/v1/admin/departments/{departmentID}/resources", s.createResource)
		r.Patch("/api/v1/admin/departments/{departmentID}/resources/{resourceID}", s.patchResource)
		r.Post("/api/v1/admin/departments/{departmentID}/schedule-preview", s.previewSchedule)
		r.Post("/api/v1/admin/departments/{departmentID}/tariff-preview", s.previewTariffs)
		r.Patch("/api/v1/admin/resource-blocks/{blockID}", s.patchBlock)

	})
}

func (s *Service) config(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "departmentID")
	key := "booking:config:v4:" + id
	if b, e := s.Redis.Get(r.Context(), key).Bytes(); e == nil {
		var cached Department
		if json.Unmarshal(b, &cached) == nil {
			platform.JSON(w, 200, salesView(cached, time.Now()))
			return
		}
	}
	d, e := department(r.Context(), s.DB, id, false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	_ = s.Redis.Set(r.Context(), key, bytesJSON(d), 30*time.Second).Err()
	platform.JSON(w, 200, salesView(d, d.ServerTime))
}
func (s *Service) resourceList(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "departmentID")
	d, e := department(r.Context(), s.DB, id, false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	a, e := resources(r.Context(), s.DB, id)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, map[string]any{"version": d.SchemeVersion, "width": 1000, "height": 1000, "resources": a})
}
func (s *Service) available(w http.ResponseWriter, r *http.Request) {
	d, err := department(r.Context(), s.DB, chi.URLParam(r, "departmentID"), false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	date, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("date"), Location)
	if err != nil {
		platform.Fail(w, platform.Bad("date должен иметь формат YYYY-MM-DD"))
		return
	}
	duration, err := durationQuery(r, d)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	now, err := nowDB(r.Context(), s.DB)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	items, err := s.availabilityItems(r.Context(), d, date, duration, now)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"department_id": d.ID, "date": date.Format("2006-01-02"), "server_time": now, "items": items})
}

func Start(ctx context.Context) error {
	c, e := platform.Load("booking")
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
	s := &Service{DB: db, Redis: rc, Config: c}
	work, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }()
	wg.Add(4)
	go func() { defer wg.Done(); platform.RunPublisher(work, db, c) }()
	go func() { defer wg.Done(); platform.RunConsumer(work, c, "booking.v1", s.HandleEvent) }()
	go func() { defer wg.Done(); s.RunExpiry(work) }()
	go func() { defer wg.Done(); platform.RunReceiptCleanup(work, db, s.PurgeContacts) }()
	r := platform.Router(c, db, rc)
	s.Routes(r)
	return platform.Serve(ctx, c.Addr, r)
}
