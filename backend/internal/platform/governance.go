package platform

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/redis/go-redis/v9"
)

type StartupError struct{ Stage string }

func (e *StartupError) Error() string { return e.Stage }
func SafeStartupError(err error) string {
	var safe *StartupError
	if errors.As(err, &safe) {
		return safe.Stage
	}
	return fmt.Sprintf("startup_failed (%T)", err)
}
func Positive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
func Busy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	Fail(w, Err(503, "server_busy", "Сервис занят; повторите запрос позже"))
}
func Admission(maximum int) func(http.Handler) http.Handler {
	slots := make(chan struct{}, Positive(maximum, 64))
	heavy := make(chan struct{}, 2)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				if heavyRequest(r) {
					select {
					case heavy <- struct{}{}:
						defer func() { <-heavy }()
					default:
						Busy(w)
						return
					}
				}
				next.ServeHTTP(w, r)
			default:
				Busy(w)
			}
		})
	}
}

var requestRateScript = redis.NewScript(`local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],60) end; return n`)

func heavyRequest(r *http.Request) bool {
	for _, suffix := range []string{"/pdf", "/availability-calendar", "/availability", "/history", "/admin/orders", "/admin/orders/search", "/occupancy", "/security-events"} {
		if strings.HasSuffix(r.URL.Path, suffix) {
			return true
		}
	}
	return false
}
func rateBudget(r *http.Request, c Config) (string, int) {
	if heavyRequest(r) {
		return "heavy", Positive(c.HeavyRatePerMinute, 60)
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodPut || r.Method == http.MethodDelete {
		return "write", Positive(c.WriteRatePerMinute, 300)
	}
	return "read", Positive(c.PublicRatePerMinute, 1200)
}
func RateLimit(c Config, rc *redis.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rc == nil || !strings.HasPrefix(r.URL.Path, "/api/") || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			group, maximum := rateBudget(r, c)
			count, err := requestRateScript.Run(r.Context(), rc, []string{"http:rate:v1:" + group + ":" + ClientKey(r, c.InternalKey)}).Int()
			if err != nil {
				if group != "read" {
					Fail(w, Err(503, "rate_limit_unavailable", "Защита операций временно недоступна"))
					return
				}
			} else if count > maximum {
				w.Header().Set("Retry-After", "60")
				Fail(w, Err(429, "rate_limited", "Слишком много запросов"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
