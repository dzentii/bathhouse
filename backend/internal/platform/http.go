package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var Validate = validator.New(validator.WithRequiredStructEnabled())

type APIError struct {
	Status  int           `json:"-"`
	Code    string        `json:"code"`
	Message string        `json:"message"`
	Details *ErrorDetails `json:"details,omitempty"`
}

// ErrorDetails contains only public resource identifiers, never booking or buyer data.
type ErrorDetails struct {
	ResourceIDs []string `json:"resource_ids"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }
func Err(status int, code, msg string) error {
	return &APIError{Status: status, Code: code, Message: msg}
}
func Conflict(code, msg string) error { return Err(409, code, msg) }
func Bad(msg string) error            { return Err(400, "invalid_request", msg) }

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func Fail(w http.ResponseWriter, err error) {
	var a *APIError
	if !errors.As(err, &a) {
		// Database and remote errors may contain DSNs or buyer details. Never log their raw text.
		slog.Error("request failed", "error_type", "internal")
		a = &APIError{Status: 500, Code: "internal_error", Message: "Внутренняя ошибка сервера"}
	}
	JSON(w, a.Status, map[string]any{"error": a})
}
func Decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return Bad("Некорректное JSON-тело запроса")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Bad("Ожидается одно JSON-значение")
	}
	if err := Validate.Struct(v); err != nil {
		return Bad("Обязательные поля отсутствуют или имеют недопустимый формат")
	}
	return nil
}
func Bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if strings.HasPrefix(v, "Bearer ") {
		return strings.TrimPrefix(v, "Bearer ")
	}
	return ""
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func RandomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("cryptographic random source unavailable")
	}
	return hex.EncodeToString(b)
}
func Equal(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func Internal(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !Equal(r.Header.Get("X-Internal-Key"), key) {
				Fail(w, Err(401, "unauthorized", "Доступ запрещён"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func serverRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Client metadata may contain secrets or arbitrarily long strings. Only
		// an independently generated identifier may enter the request log.
		id := uuid.NewString()
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), middleware.RequestIDKey, id)))
	})
}

func Router(c Config, db *pgxpool.Pool, clients ...*redis.Client) *chi.Mux {
	r := chi.NewRouter()
	metrics := &httpMetrics{}
	r.Use(metrics.middleware)
	r.Use(serverRequestID, middleware.Recoverer, middleware.Timeout(15*time.Second))
	var rc *redis.Client
	if len(clients) > 0 {
		rc = clients[0]
	}
	r.Use(ClientAddressMiddleware(c), Admission(c.HTTPMaxConcurrent), RateLimit(c, rc))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			if c.AllowedOrigin != "" && req.Header.Get("Origin") == c.AllowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", c.AllowedOrigin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Idempotency-Key, Idempotency-Key, X-CSRF-Token")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			}
			if req.Method == http.MethodOptions {
				w.WriteHeader(204)
				return
			}
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, req.ProtoMajor)
			next.ServeHTTP(ww, req)
			pattern := chi.RouteContext(req.Context()).RoutePattern()
			if pattern == "" {
				pattern = "unmatched"
			}
			slog.Info("http_request", "service", c.Service, "method", req.Method, "route", pattern, "status", ww.Status(), "duration_ms", time.Since(start).Milliseconds(), "request_id", middleware.GetReqID(req.Context()))
		})
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { JSON(w, 200, map[string]string{"status": "ok"}) })
	r.With(Internal(c.InternalKey)).Get("/internal/metrics", metrics.serve(c, db))
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if db.Ping(ctx) != nil {
			Fail(w, Err(503, "unavailable", "База данных недоступна"))
			return
		}
		JSON(w, 200, map[string]string{"status": "ready"})
	})
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		Fail(w, Err(404, "not_found", "Ресурс не найден"))
	})
	return r
}

func DB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 12
	cfg.MinConns = 1
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "15000"
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}
func Redis(url string) (*redis.Client, error) {
	o, e := redis.ParseURL(url)
	if e != nil {
		return nil, &StartupError{Stage: "invalid REDIS_URL configuration"}
	}
	// A stopped Docker service also loses its DNS record. Default dial retries
	// can consume the entire HTTP deadline before the PostgreSQL fallback runs.
	o.DialTimeout = 500 * time.Millisecond
	o.DialerRetries = 1
	o.ReadTimeout = 500 * time.Millisecond
	o.WriteTimeout = 500 * time.Millisecond
	o.PoolTimeout = 300 * time.Millisecond
	o.ContextTimeoutEnabled = true
	o.MaxRetries = -1
	o.PoolSize = 12
	o.MaxActiveConns = 12
	o.MaxConcurrentDials = 4
	o.PipelinePoolSize = -1
	return redis.NewClient(o), nil
}

func Run(start func(context.Context) error) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := start(ctx); err != nil {
		slog.Error("service stopped", "reason", SafeStartupError(err))
		os.Exit(1)
	}
}
func Serve(ctx context.Context, addr string, h http.Handler) error {
	s := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	errCh := make(chan error, 1)
	go func() { errCh <- s.ListenAndServe() }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		stop, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return s.Shutdown(stop)
	}
}
