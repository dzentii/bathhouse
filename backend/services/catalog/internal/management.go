package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return b }
func change(ctx context.Context, tx pgx.Tx, actor, kind, id, action string, before, after any) error {
	_, err := tx.Exec(ctx, "INSERT INTO catalog_changes(admin_id,entity_type,entity_id,action,before_value,after_value) VALUES($1,$2,$3,$4,$5,$6)", actor, kind, id, action, jsonBytes(before), jsonBytes(after))
	return err
}
func (s *Service) clearCache(ctx context.Context) {
	var cursor uint64
	for {
		keys, next, err := s.Redis.Scan(ctx, cursor, "catalog:v4:*", 100).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			if err = s.Redis.Del(ctx, keys...).Err(); err != nil {
				return
			}
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}
func (s *Service) createBath(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name" validate:"required"`
		Address     string `json:"address" validate:"required"`
		Description string `json:"description"`
		Active      bool   `json:"active"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.TrimSpace(in.Address)
	if !platform.SafeText(in.Name, 1, 200, false) || !platform.SafeText(in.Address, 1, 500, false) || !platform.SafeText(in.Description, 0, 12000, true) {
		platform.Fail(w, platform.Bad("Некорректные сведения о бане"))
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
	var id int
	if err = tx.QueryRow(r.Context(), "INSERT INTO baths(name,address,description,active) VALUES($1,$2,$3,$4) RETURNING id", in.Name, in.Address, in.Description, in.Active).Scan(&id); err != nil {
		platform.Fail(w, err)
		return
	}
	b, err := scanBath(tx.QueryRow(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.id=$1", id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = change(r.Context(), tx, a.ID, "bath", strconv.Itoa(id), "bath.create", nil, b); err != nil {
		platform.Fail(w, err)
		return
	}
	op.AfterCommit = func() { s.clearCache(r.Context()) }
	op.Commit(w, r, 201, b)
}
func (s *Service) patchBath(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version     int64   `json:"version" validate:"required,min=1"`
		Name        *string `json:"name"`
		Address     *string `json:"address"`
		Description *string `json:"description"`
		Active      *bool   `json:"active"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	id, err := bathID(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if in.Name == nil && in.Address == nil && in.Description == nil && in.Active == nil {
		platform.Fail(w, platform.Bad("Не заданы изменения"))
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
	b, err := scanBath(tx.QueryRow(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.id=$1 FOR UPDATE OF b", id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	before := b
	if b.Version != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Баня уже изменена"))
		return
	}
	if in.Name != nil {
		b.Name = strings.TrimSpace(*in.Name)
	}
	if in.Address != nil {
		b.Address = strings.TrimSpace(*in.Address)
	}
	if in.Description != nil {
		b.Description = *in.Description
	}
	if in.Active != nil {
		b.Active = *in.Active
	}
	if !platform.SafeText(b.Name, 1, 200, false) || !platform.SafeText(b.Address, 1, 500, false) || !platform.SafeText(b.Description, 0, 12000, true) {
		platform.Fail(w, platform.Bad("Некорректные сведения о бане"))
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE baths SET name=$2,address=$3,description=$4,active=$5,version=version+1 WHERE id=$1", id, b.Name, b.Address, b.Description, b.Active); err != nil {
		platform.Fail(w, err)
		return
	}
	updated, err := tx.Exec(r.Context(), "UPDATE departments SET version=version+1,sync_status='pending',sync_error='' WHERE bath_id=$1", id)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	// Close each bounded batch before emitting on the same transaction connection.
	after := ""
	for {
		rows, err := tx.Query(r.Context(), "SELECT "+deptColumns+" FROM departments WHERE bath_id=$1 AND id COLLATE \"C\">$2 ORDER BY id COLLATE \"C\" LIMIT 100", id, after)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		batch := make([]ManagedDepartment, 0, 100)
		for rows.Next() {
			d, e := scanDepartment(rows)
			if e != nil {
				rows.Close()
				platform.Fail(w, e)
				return
			}
			batch = append(batch, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			platform.Fail(w, err)
			return
		}
		for _, d := range batch {
			if err = emitDepartment(r.Context(), tx, b, d); err != nil {
				platform.Fail(w, err)
				return
			}
			after = d.ID
		}
		if len(batch) < 100 {
			break
		}
	}
	b, err = scanBath(tx.QueryRow(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.id=$1", id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = change(r.Context(), tx, a.ID, "bath", strconv.Itoa(id), "bath.update", before, b); err != nil {
		platform.Fail(w, err)
		return
	}
	status := 200
	if updated.RowsAffected() > 0 {
		status = 202
	}
	op.AfterCommit = func() { s.clearCache(r.Context()) }
	op.Commit(w, r, status, b)
}
func emitDepartment(ctx context.Context, tx pgx.Tx, b ManagedBath, d ManagedDepartment) error {
	return platform.Emit(ctx, tx, "catalog.department.updated", d.ID, platform.CatalogDepartment{ID: d.ID, BathID: b.ID, BathName: b.Name, BathAddress: b.Address, Name: d.Name, Mode: d.Mode, Active: d.Active && b.Active, Version: d.Version, Setup: d.Setup})
}
func (s *Service) validateSetup(r *http.Request, payload platform.CatalogDepartment) error {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.Config.BookingURL+"/internal/catalog/validate-department", bytes.NewReader(jsonBytes(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", s.Config.InternalKey)
	res, err := platform.Client.Do(req)
	if err != nil {
		return platform.Err(503, "booking_unavailable", "Сервис проверки конфигурации недоступен")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == 200 {
		return nil
	}
	if res.StatusCode == 400 || res.StatusCode == 409 {
		var body struct {
			Error platform.APIError `json:"error"`
		}
		if err = json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&body); err == nil && body.Error.Code != "" {
			body.Error.Status = res.StatusCode
			return &body.Error
		}
	}
	return platform.Err(503, "booking_unavailable", "Не удалось проверить конфигурацию отделения")
}
func (s *Service) createDepartment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string                 `json:"name" validate:"required"`
		Description string                 `json:"description"`
		Mode        string                 `json:"mode" validate:"required,oneof=session sauna info"`
		Active      bool                   `json:"active"`
		Setup       *platform.CatalogSetup `json:"booking_setup"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	id, err := bathID(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !platform.SafeText(in.Name, 1, 200, false) || !platform.SafeText(in.Description, 0, 12000, true) {
		platform.Fail(w, platform.Bad("Некорректное описание отделения"))
		return
	}
	if in.Mode == "info" && in.Setup != nil || in.Mode != "info" && in.Setup == nil {
		platform.Fail(w, platform.Bad("Онлайн-отделению нужна booking_setup; справочному она не задаётся"))
		return
	}
	if in.Setup != nil {
		for i := range in.Setup.Resources {
			if in.Setup.Resources[i].ID != "" {
				platform.Fail(w, platform.Bad("Идентификаторы новых ресурсов назначает сервер"))
				return
			}
			in.Setup.Resources[i].ID = uuid.NewString()
		}
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
	if err = s.validateSetup(r, platform.CatalogDepartment{ID: uuid.NewString(), BathID: id, Name: in.Name, Mode: in.Mode, Setup: in.Setup}); err != nil {
		platform.Fail(w, err)
		return
	}
	b, err := scanBath(tx.QueryRow(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.id=$1 FOR UPDATE OF b", id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var setup any
	if in.Setup != nil {
		setup = jsonBytes(in.Setup)
	}
	d, err := scanDepartment(tx.QueryRow(r.Context(), "INSERT INTO departments(id,bath_id,name,mode,description,active,booking_setup,sync_status) VALUES($1,$2,$3,$4,$5,$6,$7,'pending') RETURNING "+deptColumns, uuid.NewString(), id, in.Name, in.Mode, in.Description, in.Active, setup))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = emitDepartment(r.Context(), tx, b, d); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = change(r.Context(), tx, a.ID, "department", d.ID, "department.create", nil, d); err != nil {
		platform.Fail(w, err)
		return
	}
	op.AfterCommit = func() { s.clearCache(r.Context()) }
	op.Commit(w, r, 202, d)
}
func (s *Service) patchDepartment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version     int64   `json:"version" validate:"required,min=1"`
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Active      *bool   `json:"active"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if in.Name == nil && in.Description == nil && in.Active == nil {
		platform.Fail(w, platform.Bad("Не заданы изменения"))
		return
	}
	id := chi.URLParam(r, "departmentID")
	var parent int
	if err := s.DB.QueryRow(r.Context(), "SELECT bath_id FROM departments WHERE id=$1", id).Scan(&parent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = platform.Err(404, "not_found", "Отделение не найдено")
		}
		platform.Fail(w, err)
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
	b, err := scanBath(tx.QueryRow(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.id=$1 FOR UPDATE OF b", parent))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	d, err := scanDepartment(tx.QueryRow(r.Context(), "SELECT "+deptColumns+" FROM departments WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	before := d
	if d.Version != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Отделение уже изменено"))
		return
	}
	if in.Name != nil {
		d.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		d.Description = *in.Description
	}
	if in.Active != nil {
		d.Active = *in.Active
	}
	if !platform.SafeText(d.Name, 1, 200, false) || !platform.SafeText(d.Description, 0, 12000, true) {
		platform.Fail(w, platform.Bad("Некорректное описание отделения"))
		return
	}
	d, err = scanDepartment(tx.QueryRow(r.Context(), "UPDATE departments SET name=$2,description=$3,active=$4,version=version+1,sync_status='pending',sync_error='' WHERE id=$1 RETURNING "+deptColumns, id, d.Name, d.Description, d.Active))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = emitDepartment(r.Context(), tx, b, d); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = change(r.Context(), tx, a.ID, "department", id, "department.update", before, d); err != nil {
		platform.Fail(w, err)
		return
	}
	op.AfterCommit = func() { s.clearCache(r.Context()) }
	op.Commit(w, r, 202, d)
}
func (s *Service) HandleEvent(ctx context.Context, e platform.Event) error {
	if e.Type != "booking.catalog_applied" {
		return nil
	}
	var a platform.CatalogApplied
	if err := json.Unmarshal(e.Data, &a); err != nil {
		return err
	}
	if a.DepartmentID == "" || a.Version < 1 {
		return errors.New("invalid catalog acknowledgment")
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
	status := "ready"
	if a.Error != "" {
		status = "error"
	}
	if _, err = tx.Exec(ctx, "UPDATE departments SET sync_status=$3,sync_error=$4 WHERE id=$1 AND version=$2", a.DepartmentID, a.Version, status, a.Error); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.clearCache(ctx)
	return nil
}
