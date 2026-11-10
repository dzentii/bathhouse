package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Bath struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Description string `json:"description"`
	Version     int64  `json:"version"`
}
type Department struct {
	Version     int64  `json:"version"`
	ID          string `json:"id"`
	BathID      int    `json:"bath_id"`
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	Description string `json:"description"`
}
type Service struct {
	Config platform.Config
	DB     *pgxpool.Pool
	Redis  *redis.Client
}

func (s *Service) cached(ctx context.Context, key string, load func() (any, error)) (json.RawMessage, error) {
	if b, e := s.Redis.Get(ctx, "catalog:v4:"+key).Bytes(); e == nil && json.Valid(b) {
		return b, nil
	}
	v, e := load()
	if e != nil {
		return nil, e
	}
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	_ = s.Redis.Set(ctx, "catalog:v4:"+key, b, time.Minute).Err()
	return b, nil
}
func (s *Service) Routes(r chi.Router) {
	r.Get("/api/v1/baths", s.publicBaths)
	r.Get("/api/v1/baths/{bathID}", s.publicBath)
	r.Get("/api/v1/baths/{bathID}/departments", s.publicDepartments)
	r.Get("/api/v1/departments/{departmentID}", s.publicDepartment)
	r.Group(func(r chi.Router) {
		r.Use(platform.AdminOnly(s.Config), platform.RequireIdempotency(s.Config.InternalKey))
		r.Get("/api/v1/admin/baths", s.adminBaths)
		r.Post("/api/v1/admin/baths", s.createBath)
		r.Get("/api/v1/admin/baths/{bathID}", s.adminBath)
		r.Patch("/api/v1/admin/baths/{bathID}", s.patchBath)
		r.Get("/api/v1/admin/baths/{bathID}/departments", s.adminDepartments)
		r.Post("/api/v1/admin/baths/{bathID}/departments", s.createDepartment)
		r.Get("/api/v1/admin/departments/{departmentID}", s.adminDepartment)
		r.Patch("/api/v1/admin/departments/{departmentID}", s.patchDepartment)
		r.Post("/api/v1/admin/departments/{departmentID}/sync-retry", s.syncRetry)
		r.Get("/api/v1/admin/departments/{departmentID}/info", s.adminInfo)
		r.Put("/api/v1/admin/departments/{departmentID}/info", s.saveInfo)
	})
	r.Group(func(r chi.Router) {
		r.Use(platform.Internal(s.Config.InternalKey))
		r.Get("/internal/departments/{departmentID}/history", s.internalHistory)
	})
}
func Start(ctx context.Context) error {
	c, e := platform.Load("catalog")
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
	s := &Service{Config: c, DB: db, Redis: rc}
	work, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }()
	wg.Add(3)
	go func() {
		defer wg.Done()
		platform.RunReceiptCleanup(work, db, func(ctx context.Context) error { return platform.CleanupDelivery(ctx, db, c) })
	}()
	go func() { defer wg.Done(); platform.RunPublisher(work, db, c) }()
	go func() { defer wg.Done(); platform.RunConsumer(work, c, "catalog.v1", s.HandleEvent) }()
	r := platform.Router(c, db, rc)
	s.Routes(r)
	return platform.Serve(ctx, c.Addr, r)
}
