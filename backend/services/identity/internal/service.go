package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	DB     *pgxpool.Pool
	Redis  *redis.Client
	Config platform.Config
}

func (s *Service) Bootstrap(ctx context.Context) error {
	var exists bool
	if err := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM admins WHERE lower(username)=lower($1))", s.Config.AdminUser).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	h, err := bcrypt.GenerateFromPassword([]byte(s.Config.AdminPassword), 12)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, "INSERT INTO admins(id,username,password_hash,display_name,role) VALUES($1,$2,$3,$2,CASE WHEN EXISTS(SELECT 1 FROM admins WHERE role='owner' AND active) THEN 'admin' ELSE 'owner' END) ON CONFLICT(username) DO NOTHING", uuid.NewString(), s.Config.AdminUser, string(h))
	return err
}

func (s *Service) Routes(r chi.Router) {
	s.managementRoutes(r)
	r.With(platform.RequireIdempotency(s.Config.InternalKey)).Post("/api/v1/admin/sessions", s.login)
	r.Delete("/api/v1/admin/sessions/current", s.logout)
	r.Group(func(r chi.Router) {
		r.Use(platform.Internal(s.Config.InternalKey))
		r.Get("/internal/sessions/validate", s.validate)
	})
}

var rateScript = redis.NewScript(`local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],ARGV[1]) end; return n`)

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username" validate:"required,min=3,max=80"`
		Password string `json:"password" validate:"required,min=12,max=72"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if !validPassword(in.Password) {
		platform.Fail(w, platform.Bad("Пароль должен содержать 12–72 байта"))
		return
	}
	ip := platform.ClientAddress(r)
	ipCount, ipErr := rateScript.Run(r.Context(), s.Redis, []string{"identity:login-ip:" + platform.Hash(ip)}, 60).Int()
	if ipErr != nil {
		platform.Fail(w, platform.Err(503, "rate_limit_unavailable", "Вход временно недоступен"))
		return
	}
	if ipCount > 20 {
		w.Header().Set("Retry-After", "60")
		platform.Fail(w, platform.Err(429, "rate_limited", "Слишком много попыток входа"))
		return
	}
	key := "identity:login:" + platform.Hash(strings.ToLower(in.Username))
	n, err := rateScript.Run(r.Context(), s.Redis, []string{key}, 60).Int()
	if err != nil {
		platform.Fail(w, platform.Err(503, "rate_limit_unavailable", "Вход временно недоступен"))
		return
	}
	if n > 5 {
		w.Header().Set("Retry-After", "60")
		platform.Fail(w, platform.Err(429, "rate_limited", "Слишком много попыток входа"))
		return
	}
	var id, hash string
	err = s.DB.QueryRow(r.Context(), "SELECT id::text,password_hash FROM admins WHERE lower(username)=lower($1) AND active", in.Username).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = bcrypt.GenerateFromPassword([]byte("constant-time-dummy-password"), 12)
		platform.Fail(w, platform.Err(401, "invalid_credentials", "Неверный логин или пароль"))
		return
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		platform.Fail(w, platform.Err(401, "invalid_credentials", "Неверный логин или пароль"))
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var lockedHash string
	if err = tx.QueryRow(r.Context(), "SELECT password_hash FROM admins WHERE id=$1 AND active FOR UPDATE", id).Scan(&lockedHash); err != nil || lockedHash != hash {
		platform.Fail(w, platform.Err(401, "invalid_credentials", "Неверный логин или пароль"))
		return
	}
	op, err := platform.LoadOperation(r, tx, "login:"+id, s.Config.InternalKey)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	op.TTL = 8 * time.Hour
	if body := op.ReplayBody(); body != nil {
		var old struct {
			Token string `json:"access_token"`
		}
		if err = json.Unmarshal(body, &old); err != nil {
			platform.Fail(w, err)
			return
		}
		var active bool
		if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash=$1 AND expires_at>clock_timestamp())", platform.Hash(old.Token)).Scan(&active); err != nil {
			platform.Fail(w, err)
			return
		}
		if !active {
			platform.Fail(w, platform.Err(401, "session_revoked", "Сессия завершена; для нового входа нужен новый ключ"))
			return
		}
		if op.Replay(w, r) {
			return
		}
	}
	token, csrf := platform.RandomToken(), platform.RandomToken()
	var until time.Time
	err = tx.QueryRow(r.Context(), "INSERT INTO sessions(token_hash,admin_id,csrf_hash,expires_at,user_agent) VALUES($1,$2,$3,clock_timestamp()+interval '8 hours',$4) RETURNING expires_at", platform.Hash(token), id, platform.Hash(csrf), safeAgent(r.UserAgent())).Scan(&until)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	_ = s.Redis.Del(r.Context(), key).Err()
	// #nosec G124 -- COOKIE_SECURE=true is enforced for production at startup; local development uses HTTP.
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Value: token, Path: "/api/v1/admin", HttpOnly: true, Secure: s.Config.SecureCookie, SameSite: http.SameSiteStrictMode, Expires: until, MaxAge: 8 * 3600})
	op.Commit(w, r, 201, map[string]any{"access_token": token, "token_type": "Bearer", "csrf_token": csrf, "expires_at": until})
}
func (s *Service) session(r *http.Request) (platform.Admin, string, error) {
	token := platform.Bearer(r)
	cookie := false
	if token == "" {
		if ck, e := r.Cookie("admin_session"); e == nil {
			token = ck.Value
			cookie = true
		}
	}
	var a platform.Admin
	var csrf string
	if len(token) != 64 {
		return a, "", platform.Err(401, "unauthorized", "Сессия недействительна")
	}
	err := s.DB.QueryRow(r.Context(), "SELECT a.id::text,a.username,a.role,s.id::text,s.csrf_hash FROM sessions s JOIN admins a ON a.id=s.admin_id WHERE s.token_hash=$1 AND s.expires_at>clock_timestamp() AND a.active", platform.Hash(token)).Scan(&a.ID, &a.Username, &a.Role, &a.SessionID, &csrf)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, "", platform.Err(401, "unauthorized", "Сессия недействительна")
	}
	if err != nil {
		return a, "", err
	}
	if r.Header.Get("X-Check-CSRF") == "true" || (cookie && r.Method != http.MethodGet) {
		if !platform.Equal(platform.Hash(r.Header.Get("X-CSRF-Token")), csrf) {
			return a, "", platform.Err(403, "csrf_required", "Требуется CSRF-токен")
		}
		origin := r.Header.Get("Origin")
		if origin != "" && origin != s.Config.PublicURL && origin != s.Config.AllowedOrigin {
			return a, "", platform.Err(403, "forbidden", "Недопустимый источник запроса")
		}
	}
	return a, token, nil
}
func (s *Service) validate(w http.ResponseWriter, r *http.Request) {
	a, _, err := s.session(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, a)
}
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	_, token, err := s.session(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if _, err = s.DB.Exec(r.Context(), "DELETE FROM sessions WHERE token_hash=$1", platform.Hash(token)); err != nil {
		platform.Fail(w, err)
		return
	}
	// #nosec G124 -- COOKIE_SECURE=true is enforced for production at startup; local development uses HTTP.
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Path: "/api/v1/admin", Value: "", MaxAge: -1, HttpOnly: true, Secure: s.Config.SecureCookie, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(204)
}

func Start(ctx context.Context) error {
	c, err := platform.Load("identity")
	if err != nil {
		return err
	}
	db, err := platform.DB(ctx, c.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database unavailable")
	}
	defer db.Close()
	rc, err := platform.Redis(c.RedisURL)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	s := &Service{db, rc, c}
	if err = s.Bootstrap(ctx); err != nil {
		return fmt.Errorf("cannot bootstrap administrator")
	}
	work, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		platform.RunReceiptCleanup(work, db, func(ctx context.Context) error {
			for {
				tag, err := db.Exec(ctx, "DELETE FROM sessions WHERE token_hash IN(SELECT token_hash FROM sessions WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED)")
				if err != nil {
					return err
				}
				if tag.RowsAffected() < 500 {
					return nil
				}
			}
		})
	}()
	r := platform.Router(c, db, rc)
	s.Routes(r)
	return platform.Serve(ctx, c.Addr, r)
}
