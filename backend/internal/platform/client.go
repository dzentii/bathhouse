package platform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type Admin struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	SessionID string `json:"session_id"`
}
type adminKey struct{}

func WithAdmin(ctx context.Context, a Admin) context.Context {
	return context.WithValue(ctx, adminKey{}, a)
}
func OwnerOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CurrentAdmin(r.Context()).Role != "owner" {
			Fail(w, Err(403, "forbidden", "Требуются права владельца"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func CurrentAdmin(ctx context.Context) Admin { a, _ := ctx.Value(adminKey{}).(Admin); return a }

var Client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

func AdminOnly(c Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := Bearer(r)
			cookieAuth := false
			if token == "" {
				if ck, e := r.Cookie("admin_session"); e == nil {
					token = ck.Value
					cookieAuth = true
				}
			}
			if token == "" {
				Fail(w, Err(401, "unauthorized", "Требуется вход администратора"))
				return
			}
			req, e := http.NewRequestWithContext(r.Context(), http.MethodGet, c.IdentityURL+"/internal/sessions/validate", nil)
			if e != nil {
				Fail(w, e)
				return
			}
			req.Header.Set("X-Internal-Key", c.InternalKey)
			req.Header.Set("Authorization", "Bearer "+token)
			if cookieAuth && r.Method != http.MethodGet && r.Method != http.MethodHead {
				if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != c.AllowedOrigin && r.Header.Get("Origin") != c.PublicURL {
					Fail(w, Err(403, "forbidden", "Недопустимый источник запроса"))
					return
				}
				req.Header.Set("X-CSRF-Token", r.Header.Get("X-CSRF-Token"))
				req.Header.Set("X-Check-CSRF", "true")
			}
			res, e := Client.Do(req)
			if e != nil {
				Fail(w, Err(503, "identity_unavailable", "Сервис доступа недоступен"))
				return
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != 200 {
				code := res.StatusCode
				if code != 401 && code != 403 {
					code = 503
				}
				Fail(w, Err(code, "unauthorized", "Действительный доступ не подтверждён"))
				return
			}
			var a Admin
			if e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&a); e != nil {
				Fail(w, e)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminKey{}, a)))
		})
	}
}
