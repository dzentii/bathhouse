package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type Employee struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	Active      bool      `json:"active"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
}

const employeeColumns = "id::text,username,display_name,role,active,version,created_at"

func employee(row pgx.Row) (Employee, error) {
	var e Employee
	err := row.Scan(&e.ID, &e.Username, &e.DisplayName, &e.Role, &e.Active, &e.Version, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = platform.Err(404, "not_found", "Сотрудник не найден")
	}
	return e, err
}
func (s *Service) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, _, err := s.session(r)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(platform.WithAdmin(r.Context(), a)))
	})
}
func (s *Service) managementRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.authenticate, platform.RequireIdempotency(s.Config.InternalKey))
		r.Get("/api/v1/admin/me", s.me)
		r.Put("/api/v1/admin/me/password", s.password)
		r.Get("/api/v1/admin/sessions", s.sessions)
		r.Delete("/api/v1/admin/sessions/{sessionID}", s.revokeSession)
		r.Group(func(r chi.Router) {
			r.Use(platform.OwnerOnly)
			r.Get("/api/v1/admin/employees", s.employees)
			r.Get("/api/v1/admin/employees/{employeeID}", s.employeeCard)
			r.Get("/api/v1/admin/security-events", s.securityEvents)
			r.Post("/api/v1/admin/employees", s.createEmployee)
			r.Patch("/api/v1/admin/employees/{employeeID}", s.patchEmployee)
		})
	})
}
func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	e, err := employee(s.DB.QueryRow(r.Context(), "SELECT "+employeeColumns+" FROM admins WHERE id=$1", platform.CurrentAdmin(r.Context()).ID))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"employee": e, "session_id": platform.CurrentAdmin(r.Context()).SessionID})
}
func (s *Service) sessions(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := platform.Page(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), "SELECT id::text,created_at,expires_at,user_agent FROM sessions WHERE admin_id=$1 AND expires_at>clock_timestamp() ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3", platform.CurrentAdmin(r.Context()).ID, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, agent string
		var created, expires time.Time
		if err = rows.Scan(&id, &created, &expires, &agent); err != nil {
			platform.Fail(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "created_at": created, "expires_at": expires, "user_agent": agent, "current": id == platform.CurrentAdmin(r.Context()).SessionID})
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset})
}
func (s *Service) revokeSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sessionID")
	if uuid.Validate(id) != nil {
		platform.Fail(w, platform.Bad("Некорректная сессия"))
		return
	}
	res, err := s.DB.Exec(r.Context(), "DELETE FROM sessions WHERE id=$1 AND admin_id=$2", id, platform.CurrentAdmin(r.Context()).ID)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if res.RowsAffected() == 0 {
		platform.Fail(w, platform.Err(404, "not_found", "Сессия не найдена"))
		return
	}
	w.WriteHeader(204)
}
func validPassword(value string) bool { return len(value) >= 12 && len(value) <= 72 }
func (s *Service) password(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current_password" validate:"required"`
		New     string `json:"new_password" validate:"required"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if !validPassword(in.Current) || !validPassword(in.New) {
		platform.Fail(w, platform.Bad("Пароль должен содержать 12–72 байта"))
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	a := platform.CurrentAdmin(r.Context())
	op, err := platform.LoadOperation(r, tx, "admin:"+a.ID, s.Config.InternalKey)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if op.Replay(w, r) {
		return
	}
	var old string
	if err = tx.QueryRow(r.Context(), "SELECT password_hash FROM admins WHERE id=$1 AND active FOR UPDATE", a.ID).Scan(&old); err != nil {
		platform.Fail(w, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(old), []byte(in.Current)) != nil {
		platform.Fail(w, platform.Err(403, "invalid_password", "Текущий пароль неверен"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.New), 12)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE admins SET password_hash=$2,version=version+1 WHERE id=$1", a.ID, string(hash)); err != nil {
		platform.Fail(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "DELETE FROM sessions WHERE admin_id=$1 AND id<>$2", a.ID, a.SessionID); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = employeeAuditDetails(r.Context(), tx, a.ID, a.ID, "password.change", nil, map[string]bool{"password_changed": true}); err != nil {
		platform.Fail(w, err)
		return
	}
	op.Commit(w, r, 200, map[string]any{"changed": true, "other_sessions_revoked": true})
}
func (s *Service) employees(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := platform.Page(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), "SELECT "+employeeColumns+" FROM admins ORDER BY created_at,id LIMIT $1 OFFSET $2", limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	items := []Employee{}
	for rows.Next() {
		e, err := employee(rows)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		items = append(items, e)
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset})
}
func (s *Service) createEmployee(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username    string `json:"username" validate:"required,min=3,max=80"`
		DisplayName string `json:"display_name" validate:"required,min=1,max=100"`
		Password    string `json:"password" validate:"required"`
		Role        string `json:"role" validate:"required,oneof=owner admin"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if !validPassword(in.Password) || !platform.SafeText(in.Username, 3, 80, false) || !platform.SafeText(in.DisplayName, 1, 100, false) {
		platform.Fail(w, platform.Bad("Некорректные имя, логин или пароль"))
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	a := platform.CurrentAdmin(r.Context())
	op, err := platform.LoadOperation(r, tx, "admin:"+a.ID, s.Config.InternalKey)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if op.Replay(w, r) {
		return
	}
	if err = ownerTransaction(r.Context(), tx, a); err != nil {
		platform.Fail(w, err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	e, err := employee(tx.QueryRow(r.Context(), "INSERT INTO admins(id,username,display_name,password_hash,role) VALUES($1,$2,$3,$4,$5) RETURNING "+employeeColumns, uuid.NewString(), in.Username, in.DisplayName, string(hash), in.Role))
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" {
			err = platform.Conflict("username_exists", "Такой логин уже занят")
		}
		platform.Fail(w, err)
		return
	}
	if err = employeeAuditDetails(r.Context(), tx, a.ID, e.ID, "employee.create", nil, auditState(e)); err != nil {
		platform.Fail(w, err)
		return
	}
	op.Commit(w, r, 201, e)
}
func (s *Service) patchEmployee(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version     int64   `json:"version" validate:"required,min=1"`
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		Active      *bool   `json:"active"`
		Password    *string `json:"password"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	id := chi.URLParam(r, "employeeID")
	a := platform.CurrentAdmin(r.Context())
	parsedID, parseErr := uuid.Parse(id)
	if parseErr != nil || in.DisplayName == nil && in.Role == nil && in.Active == nil && in.Password == nil {
		platform.Fail(w, platform.Bad("Нужны корректный сотрудник и изменения"))
		return
	}
	// UUID aliases must identify the same employee before permission comparisons.
	id = parsedID.String()
	if in.Password != nil && (id == a.ID || !validPassword(*in.Password)) {
		platform.Fail(w, platform.Bad("Свой пароль меняется через /admin/me/password; новый пароль должен содержать 12–72 байта"))
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	op, err := platform.LoadOperation(r, tx, "admin:"+a.ID, s.Config.InternalKey)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if op.Replay(w, r) {
		return
	}
	if err = ownerTransaction(r.Context(), tx, a); err != nil {
		platform.Fail(w, err)
		return
	}
	e, err := employee(tx.QueryRow(r.Context(), "SELECT "+employeeColumns+" FROM admins WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if e.Version != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Сотрудник уже изменён"))
		return
	}
	before := e
	if in.DisplayName != nil {
		e.DisplayName = strings.TrimSpace(*in.DisplayName)
	}
	if in.Role != nil {
		e.Role = *in.Role
	}
	if in.Active != nil {
		e.Active = *in.Active
	}
	if !platform.SafeText(e.DisplayName, 1, 100, false) || (e.Role != "owner" && e.Role != "admin") {
		platform.Fail(w, platform.Bad("Некорректные имя или роль"))
		return
	}
	if !e.Active || e.Role != "owner" {
		var owners int
		if err = tx.QueryRow(r.Context(), "SELECT count(*) FROM admins WHERE role='owner' AND active AND id<>$1", id).Scan(&owners); err != nil {
			platform.Fail(w, err)
			return
		}
		if owners == 0 {
			platform.Fail(w, platform.Conflict("last_owner", "Нельзя отключить или понизить последнего владельца"))
			return
		}
	}
	var passwordHash *string
	if in.Password != nil {
		h, err := bcrypt.GenerateFromPassword([]byte(*in.Password), 12)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		value := string(h)
		passwordHash = &value
	}
	e, err = employee(tx.QueryRow(r.Context(), "UPDATE admins SET display_name=$2,role=$3,active=$4,password_hash=COALESCE($5,password_hash),version=version+1 WHERE id=$1 RETURNING "+employeeColumns, id, e.DisplayName, e.Role, e.Active, passwordHash))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if !e.Active || in.Password != nil || in.Role != nil {
		if _, err = tx.Exec(r.Context(), "DELETE FROM sessions WHERE admin_id=$1", id); err != nil {
			platform.Fail(w, err)
			return
		}
	}
	if err = auditEmployeePatch(r.Context(), tx, a.ID, before, e, in.Password != nil); err != nil {
		platform.Fail(w, err)
		return
	}
	op.Commit(w, r, 200, e)
}

func safeAgent(value string) string {
	r := []rune(value)
	if len(r) > 200 {
		r = r[:200]
	}
	return strings.Map(func(c rune) rune {
		if c < 32 || c == 127 {
			return -1
		}
		return c
	}, string(r))
}

func ownerTransaction(ctx context.Context, tx pgx.Tx, a platform.Admin) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('identity:owners',0))"); err != nil {
		return err
	}
	var allowed bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM admins a JOIN sessions s ON s.admin_id=a.id WHERE a.id=$1 AND a.active AND a.role='owner' AND s.id=$2 AND s.expires_at>clock_timestamp())", a.ID, a.SessionID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return platform.Err(403, "forbidden", "Полномочия владельца больше не действуют")
	}
	return nil
}
