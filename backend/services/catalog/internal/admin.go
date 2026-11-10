package catalog

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"unicode"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

func (s *Service) adminInfo(w http.ResponseWriter, r *http.Request) {
	var d Department
	err := s.DB.QueryRow(r.Context(), "SELECT id,bath_id,name,mode,description,version FROM departments WHERE id=$1 AND active", chi.URLParam(r, "departmentID")).Scan(&d.ID, &d.BathID, &d.Name, &d.Mode, &d.Description, &d.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = platform.Err(404, "not_found", "Отделение не найдено")
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, d)
}

func (s *Service) saveInfo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version     int64  `json:"version" validate:"required,min=1"`
		Description string `json:"description" validate:"required,min=1,max=12000"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	for _, c := range in.Description {
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			platform.Fail(w, platform.Bad("Недопустимый символ в описании"))
			return
		}
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	op, err := platform.LoadOperation(r, tx, "admin:"+platform.CurrentAdmin(r.Context()).ID, s.Config.InternalKey)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if op.Replay(w, r) {
		return
	}
	var d Department
	err = tx.QueryRow(r.Context(), "SELECT id,bath_id,name,mode,description,version FROM departments WHERE id=$1 AND active FOR UPDATE", chi.URLParam(r, "departmentID")).Scan(&d.ID, &d.BathID, &d.Name, &d.Mode, &d.Description, &d.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = platform.Err(404, "not_found", "Отделение не найдено")
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var sync string
	if err = tx.QueryRow(r.Context(), "SELECT sync_status FROM departments WHERE id=$1", d.ID).Scan(&sync); err != nil {
		platform.Fail(w, err)
		return
	}
	if sync != "ready" {
		platform.Fail(w, platform.Conflict("sync_pending", "Дождитесь завершения синхронизации отделения"))
		return
	}
	if d.Version != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Описание уже изменено; загрузите актуальную версию"))
		return
	}
	before, _ := json.Marshal(d)
	d.Description = in.Description
	d.Version++
	_, err = tx.Exec(r.Context(), "UPDATE departments SET description=$2,version=version+1 WHERE id=$1", d.ID, d.Description)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	after, _ := json.Marshal(d)
	_, err = tx.Exec(r.Context(), "INSERT INTO catalog_audit(admin_id,department_id,before_value,after_value) VALUES($1,$2,$3,$4)", platform.CurrentAdmin(r.Context()).ID, d.ID, before, after)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	op.AfterCommit = func() {
		_ = s.Redis.Del(r.Context(), "catalog:v4:department:"+d.ID, "catalog:v4:bath:"+strconv.Itoa(d.BathID)).Err()
	}
	op.Commit(w, r, 200, d)
}
