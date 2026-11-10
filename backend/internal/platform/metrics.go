package platform

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

var deliveryFailures, deadLetters atomic.Uint64

// Fixed buckets and status classes avoid labels derived from user input.
type httpMetrics struct {
	active    atomic.Int64
	counts    [6]atomic.Uint64
	duration  [6]atomic.Uint64
	lastAlert atomic.Int64
}

func (m *httpMetrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() && r.URL.Path == "/internal/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		m.active.Add(1)
		defer m.active.Add(-1)
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = 200
		}
		class := min(max(status/100, 0), 5)
		m.counts[class].Add(1)
		elapsed := time.Since(start).Seconds()
		for i, upper := range []float64{0.1, 0.5, 1, 5, 15, 1e12} {
			if elapsed <= upper {
				m.duration[i].Add(1)
				break
			}
		}
	})
}
func (m *httpMetrics) serve(c Config, db *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		var pending int64
		var oldest float64
		if c.Service != "identity" {
			if err := db.QueryRow(ctx, `SELECT count(*),COALESCE(EXTRACT(epoch FROM clock_timestamp()-min(created_at)),0) FROM outbox WHERE delivered_at IS NULL`).Scan(&pending, &oldest); err != nil {
				Fail(w, Err(503, "metrics_unavailable", "Метрики временно недоступны"))
				return
			}
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		stats := db.Stat()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		for _, v := range []struct {
			name  string
			value any
		}{
			{"banya_http_active", m.active.Load()}, {"banya_heap_bytes", mem.HeapAlloc}, {"banya_goroutines", runtime.NumGoroutine()},
			{"banya_db_acquired", stats.AcquiredConns()}, {"banya_db_max", stats.MaxConns()},
			{"banya_outbox_pending", pending}, {"banya_outbox_oldest_seconds", oldest},
			{"banya_delivery_failures_total", deliveryFailures.Load()}, {"banya_dead_letters_total", deadLetters.Load()},
		} {
			_, _ = fmt.Fprintf(w, "%s %v\n", v.name, v.value)
		}
		for i := range m.counts {
			_, _ = fmt.Fprintf(w, "banya_http_responses_total{class=\"%dxx\"} %d\n", i, m.counts[i].Load())
		}
		var cumulative uint64
		for i, upper := range []string{"0.1", "0.5", "1", "5", "15", "+Inf"} {
			cumulative += m.duration[i].Load()
			_, _ = fmt.Fprintf(w, "banya_http_duration_seconds_bucket{le=\"%s\"} %d\n", upper, cumulative)
		}
		if oldest > 120 || mem.HeapAlloc > 160<<20 || stats.AcquiredConns() == stats.MaxConns() {
			previous := m.lastAlert.Load()
			now := time.Now().Unix()
			if now-previous >= 60 && m.lastAlert.CompareAndSwap(previous, now) {
				slog.Warn("operational_threshold_exceeded", "service", c.Service, "outbox_age_seconds", oldest, "heap_bytes", mem.HeapAlloc, "db_acquired", stats.AcquiredConns())
			}
		}
	}
}
