package booking

import (
	"net/http"
	"strings"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ManagedResource struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Active  bool   `json:"active"`
	Version int64  `json:"version"`
}

func scanResource(row pgx.Row) (ManagedResource, error) {
	var a ManagedResource
	err := row.Scan(&a.ID, &a.Label, &a.X, &a.Y, &a.Active, &a.Version)
	if err == pgx.ErrNoRows {
		err = platform.Err(404, "not_found", "Ресурс не найден")
	}
	return a, err
}
func validResource(a ManagedResource) bool {
	return platform.SafeText(a.Label, 1, 80, false) && a.X >= 0 && a.X <= 1000 && a.Y >= 0 && a.Y <= 1000
}
func (s *Service) adminResources(w http.ResponseWriter, r *http.Request) {
	d, err := department(r.Context(), s.DB, chi.URLParam(r, "departmentID"), false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	limit, offset, err := platform.Page(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), "SELECT id,label,x,y,active,version FROM resources WHERE department_id=$1 ORDER BY length(label),label COLLATE \"C\",id COLLATE \"C\" LIMIT $2 OFFSET $3", d.ID, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	items := []ManagedResource{}
	for rows.Next() {
		a, err := scanResource(rows)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		items = append(items, a)
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"resources": items, "version": d.SchemeVersion, "width": 1000, "height": 1000, "limit": limit, "offset": offset})
}
func (s *Service) createResource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label         string `json:"label" validate:"required"`
		X             int    `json:"x"`
		Y             int    `json:"y"`
		SchemeVersion int64  `json:"scheme_version" validate:"required,min=1"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	a := ManagedResource{ID: uuid.NewString(), Label: strings.TrimSpace(in.Label), X: in.X, Y: in.Y, Active: true, Version: 1}
	if !validResource(a) {
		platform.Fail(w, platform.Bad("Некорректные метка или координаты"))
		return
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
	d, err := department(r.Context(), tx, chi.URLParam(r, "departmentID"), true)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if d.SchemeVersion != in.SchemeVersion {
		platform.Fail(w, platform.Conflict("version_conflict", "Схема уже изменена"))
		return
	}
	var count int
	if err = tx.QueryRow(r.Context(), "SELECT count(*) FROM resources WHERE department_id=$1 AND active", d.ID).Scan(&count); err != nil {
		platform.Fail(w, err)
		return
	}
	if count >= 500 || d.Mode == "sauna" && count > 0 {
		platform.Fail(w, platform.Conflict("resource_limit", "Нельзя добавить ещё один активный ресурс в эту схему"))
		return
	}
	var exists bool
	if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM resources WHERE department_id=$1 AND label=$2)", d.ID, a.Label).Scan(&exists); err != nil {
		platform.Fail(w, err)
		return
	}
	if exists {
		platform.Fail(w, platform.Conflict("label_exists", "Метка уже используется"))
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO resources(id,department_id,label,x,y) VALUES($1,$2,$3,$4,$5)", a.ID, d.ID, a.Label, a.X, a.Y); err != nil {
		platform.Fail(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE departments SET scheme_version=scheme_version+1 WHERE id=$1", d.ID); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = audit(r.Context(), tx, platform.CurrentAdmin(r.Context()), "resource.create", d.ID, nil, a); err != nil {
		platform.Fail(w, err)
		return
	}
	op.AfterCommit = func() { s.invalidate(r.Context(), d.ID) }
	op.Commit(w, r, 201, map[string]any{"resource": a, "scheme_version": d.SchemeVersion + 1})
}
func (s *Service) patchResource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version int64   `json:"version" validate:"required,min=1"`
		Label   *string `json:"label"`
		X       *int    `json:"x"`
		Y       *int    `json:"y"`
		Active  *bool   `json:"active"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if in.Label == nil && in.X == nil && in.Y == nil && in.Active == nil {
		platform.Fail(w, platform.Bad("Не заданы изменения ресурса"))
		return
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
	d, err := department(r.Context(), tx, chi.URLParam(r, "departmentID"), true)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = expireDepartment(r.Context(), tx, d.ID); err != nil {
		platform.Fail(w, err)
		return
	}
	a, err := scanResource(tx.QueryRow(r.Context(), "SELECT id,label,x,y,active,version FROM resources WHERE department_id=$1 AND id=$2 FOR UPDATE", d.ID, chi.URLParam(r, "resourceID")))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	before := a
	if a.Version != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Ресурс уже изменён"))
		return
	}
	if in.Label != nil {
		a.Label = strings.TrimSpace(*in.Label)
	}
	if in.X != nil {
		a.X = *in.X
	}
	if in.Y != nil {
		a.Y = *in.Y
	}
	if in.Active != nil {
		a.Active = *in.Active
	}
	if !validResource(a) {
		platform.Fail(w, platform.Bad("Некорректные метка или координаты"))
		return
	}
	var conflict bool
	if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM allocations WHERE resource_id=$1 AND active AND upper(span)>clock_timestamp())", a.ID).Scan(&conflict); err != nil {
		platform.Fail(w, err)
		return
	}
	if conflict {
		platform.Fail(w, platform.Conflict("existing_order_conflict", "Ресурс используется действующим оформлением"))
		return
	}
	if !a.Active {
		if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM resource_blocks WHERE active AND $1=ANY(resource_ids) AND ends_at>clock_timestamp())", a.ID).Scan(&conflict); err != nil {
			platform.Fail(w, err)
			return
		}
		if conflict {
			platform.Fail(w, platform.Conflict("existing_block_conflict", "Сначала снимите действующие блокировки ресурса"))
			return
		}
	}
	if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM resources WHERE department_id=$1 AND id<>$2 AND label=$3)", d.ID, a.ID, a.Label).Scan(&conflict); err != nil {
		platform.Fail(w, err)
		return
	}
	if conflict {
		platform.Fail(w, platform.Conflict("label_exists", "Метка уже используется"))
		return
	}
	if a.Active && !before.Active {
		var count int
		if err = tx.QueryRow(r.Context(), "SELECT count(*) FROM resources WHERE department_id=$1 AND active", d.ID).Scan(&count); err != nil {
			platform.Fail(w, err)
			return
		}
		if count >= 500 {
			platform.Fail(w, platform.Conflict("resource_limit", "Достигнут предел 500 активных ресурсов"))
			return
		}
	}
	if a.Active && d.Mode == "sauna" {
		if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM resources WHERE department_id=$1 AND id<>$2 AND active)", d.ID, a.ID).Scan(&conflict); err != nil {
			platform.Fail(w, err)
			return
		}
		if conflict {
			platform.Fail(w, platform.Conflict("resource_limit", "Сауна имеет один активный ресурс"))
			return
		}
	}
	a, err = scanResource(tx.QueryRow(r.Context(), "UPDATE resources SET label=$3,x=$4,y=$5,active=$6,version=version+1 WHERE department_id=$1 AND id=$2 RETURNING id,label,x,y,active,version", d.ID, a.ID, a.Label, a.X, a.Y, a.Active))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE departments SET scheme_version=scheme_version+1 WHERE id=$1", d.ID); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = audit(r.Context(), tx, platform.CurrentAdmin(r.Context()), "resource.update", d.ID, before, a); err != nil {
		platform.Fail(w, err)
		return
	}
	op.AfterCommit = func() { s.invalidate(r.Context(), d.ID) }
	op.Commit(w, r, 200, map[string]any{"resource": a, "scheme_version": d.SchemeVersion + 1})
}
