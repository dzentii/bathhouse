package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) adminConfig(w http.ResponseWriter, r *http.Request) {
	d, e := department(r.Context(), s.DB, chi.URLParam(r, "departmentID"), false)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, salesView(d, d.ServerTime))
}
func audit(ctx context.Context, tx pgx.Tx, admin platform.Admin, action, id string, before, after any) error {
	_, e := tx.Exec(ctx, "INSERT INTO audit(admin_id,action,entity_id,before_value,after_value) VALUES($1,$2,$3,$4,$5)", admin.ID, action, id, bytesJSON(before), bytesJSON(after))
	return e
}
func (s *Service) invalidate(ctx context.Context, id string) {
	_ = s.Redis.Del(ctx, "booking:config:v4:"+id).Err()
}
func (s *Service) saveSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version  int64    `json:"version" validate:"required,min=1"`
		Schedule Schedule `json:"schedule" validate:"required"`
	}
	if e := platform.Decode(w, r, &in); e != nil {
		platform.Fail(w, e)
		return
	}
	if e := ValidateSchedule(in.Schedule); e != nil {
		platform.Fail(w, e)
		return
	}
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	op, e := platform.LoadOperation(r, tx, "admin:"+platform.CurrentAdmin(r.Context()).ID, s.Config.InternalKey)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if op.Replay(w, r) {
		return
	}
	id := chi.URLParam(r, "departmentID")
	d, e := department(r.Context(), tx, id, true)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if d.ScheduleVersion != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Расписание уже изменено; загрузите актуальную версию"))
		return
	}
	if e = expireDepartment(r.Context(), tx, id); e != nil {
		platform.Fail(w, e)
		return
	}
	proposed := d
	proposed.Schedule = in.Schedule
	rows, e := tx.Query(r.Context(), "SELECT selection,quote FROM orders WHERE department_id=$1 AND status IN ('held','confirmed') AND ends_at>clock_timestamp()", id)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	for rows.Next() {
		var sel Selection
		var q Quote
		if e = rows.Scan(&sel, &q); e != nil {
			rows.Close()
			platform.Fail(w, e)
			return
		}
		g, err := allowedSlot(proposed, sel)
		if err != nil || g != q.Gender {
			rows.Close()
			platform.Fail(w, platform.Conflict("existing_order_conflict", "Изменение нарушает действующее удержание или подтверждённый заказ"))
			return
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if _, e = tx.Exec(r.Context(), "UPDATE departments SET schedule=$2,schedule_version=schedule_version+1 WHERE id=$1", id, bytesJSON(in.Schedule)); e != nil {
		platform.Fail(w, e)
		return
	}
	if e = audit(r.Context(), tx, platform.CurrentAdmin(r.Context()), "schedule.update", id, d.Schedule, in.Schedule); e != nil {
		platform.Fail(w, e)
		return
	}
	if e = platform.Emit(r.Context(), tx, "department.changed", id, map[string]string{"department_id": id}); e != nil {
		platform.Fail(w, e)
		return
	}
	if !op.Commit(w, r, 200, map[string]any{"version": in.Version + 1, "schedule": in.Schedule}) {
		return
	}
	s.invalidate(r.Context(), id)

}
func (s *Service) saveTariffs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version int64   `json:"version" validate:"required,min=1"`
		Tariffs Tariffs `json:"tariffs" validate:"required"`
	}
	if e := platform.Decode(w, r, &in); e != nil {
		platform.Fail(w, e)
		return
	}
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	op, e := platform.LoadOperation(r, tx, "admin:"+platform.CurrentAdmin(r.Context()).ID, s.Config.InternalKey)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if op.Replay(w, r) {
		return
	}
	id := chi.URLParam(r, "departmentID")
	d, e := department(r.Context(), tx, id, true)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if d.TariffVersion != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Тариф уже изменён; загрузите актуальную версию"))
		return
	}
	if e = ValidateTariffs(in.Tariffs, d.Mode); e != nil {
		platform.Fail(w, e)
		return
	}
	if _, e = tx.Exec(r.Context(), "UPDATE departments SET tariffs=$2,tariff_version=tariff_version+1 WHERE id=$1", id, bytesJSON(in.Tariffs)); e != nil {
		platform.Fail(w, e)
		return
	}
	if e = audit(r.Context(), tx, platform.CurrentAdmin(r.Context()), "tariff.update", id, d.Tariffs, in.Tariffs); e != nil {
		platform.Fail(w, e)
		return
	}
	if e = platform.Emit(r.Context(), tx, "department.changed", id, map[string]string{"department_id": id}); e != nil {
		platform.Fail(w, e)
		return
	}
	if !op.Commit(w, r, 200, map[string]any{"version": in.Version + 1, "tariffs": in.Tariffs}) {
		return
	}
	s.invalidate(r.Context(), id)

}

type Block struct {
	ID           string    `json:"id"`
	DepartmentID string    `json:"department_id" validate:"required,max=80"`
	ResourceIDs  []string  `json:"resource_ids" validate:"required,min=1,max=100,dive,max=100"`
	StartsAt     time.Time `json:"starts_at" validate:"required"`
	EndsAt       time.Time `json:"ends_at" validate:"required"`
	Reason       string    `json:"reason" validate:"required,min=1,max=300"`
	Version      int64     `json:"version"`
	Active       bool      `json:"active"`
}

func (s *Service) block(w http.ResponseWriter, r *http.Request) {
	var b Block
	if e := platform.Decode(w, r, &b); e != nil {
		platform.Fail(w, e)
		return
	}
	if b.ID != "" || b.Version != 0 || b.Active {
		platform.Fail(w, platform.Bad("id, version и active задаются сервером"))
		return
	}
	if !b.EndsAt.After(b.StartsAt) || b.EndsAt.Sub(b.StartsAt) > 366*24*time.Hour {
		platform.Fail(w, platform.Bad("Недопустимый период блокировки"))
		return
	}
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	op, e := platform.LoadOperation(r, tx, "admin:"+platform.CurrentAdmin(r.Context()).ID, s.Config.InternalKey)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if op.Replay(w, r) {
		return
	}
	d, e := department(r.Context(), tx, b.DepartmentID, true)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if e = expireDepartment(r.Context(), tx, d.ID); e != nil {
		platform.Fail(w, e)
		return
	}
	if _, e = selectedResources(r.Context(), tx, d, Selection{ResourceIDs: b.ResourceIDs}); e != nil {
		platform.Fail(w, e)
		return
	}
	seen := map[string]bool{}
	for _, id := range b.ResourceIDs {
		if seen[id] {
			platform.Fail(w, platform.Bad("Ресурс указан дважды"))
			return
		}
		seen[id] = true
	}
	var conflict bool
	e = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM allocations WHERE active AND resource_id=ANY($1) AND span && tstzrange($2,$3,'[)'))", b.ResourceIDs, b.StartsAt, b.EndsAt).Scan(&conflict)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if conflict {
		platform.Fail(w, platform.Conflict("existing_order_conflict", "Блокировка пересекает удержание или подтверждённый заказ"))
		return
	}
	e = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM resource_blocks WHERE active AND department_id=$1 AND resource_ids && $2::text[] AND tstzrange(starts_at,ends_at,'[)') && tstzrange($3,$4,'[)'))", d.ID, b.ResourceIDs, b.StartsAt, b.EndsAt).Scan(&conflict)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if conflict {
		platform.Fail(w, platform.Conflict("block_overlap", "Для части ресурсов уже действует блокировка"))
		return
	}
	b.ID = uuid.NewString()
	b.Version = 1
	b.Active = true
	if _, e = tx.Exec(r.Context(), "INSERT INTO resource_blocks(id,department_id,resource_ids,starts_at,ends_at,reason) VALUES($1,$2,$3,$4,$5,$6)", b.ID, d.ID, b.ResourceIDs, b.StartsAt, b.EndsAt, b.Reason); e != nil {
		platform.Fail(w, e)
		return
	}
	if e = audit(r.Context(), tx, platform.CurrentAdmin(r.Context()), "resource.block", b.ID, nil, b); e != nil {
		platform.Fail(w, e)
		return
	}
	if e = platform.Emit(r.Context(), tx, "resource.blocked", d.ID, map[string]string{"department_id": d.ID, "block_id": b.ID}); e != nil {
		platform.Fail(w, e)
		return
	}
	if !op.Commit(w, r, 201, b) {
		return
	}

}
func (s *Service) unblock(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "blockID")
	version, e := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
	if uuid.Validate(id) != nil || e != nil || version < 1 {
		platform.Fail(w, platform.Bad("Нужны корректный blockID и version"))
		return
	}
	var dep string
	e = s.DB.QueryRow(r.Context(), "SELECT department_id FROM resource_blocks WHERE id=$1", id).Scan(&dep)
	if e == pgx.ErrNoRows {
		platform.Fail(w, platform.Err(404, "not_found", "Блокировка не найдена"))
		return
	}
	if e != nil {
		platform.Fail(w, e)
		return
	}
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, e = department(r.Context(), tx, dep, true); e != nil {
		platform.Fail(w, e)
		return
	}
	var b Block
	e = tx.QueryRow(r.Context(), "SELECT id::text,department_id,resource_ids,starts_at,ends_at,reason,version,active FROM resource_blocks WHERE id=$1 FOR UPDATE", id).Scan(&b.ID, &b.DepartmentID, &b.ResourceIDs, &b.StartsAt, &b.EndsAt, &b.Reason, &b.Version, &b.Active)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	if b.Version != version {
		platform.Fail(w, platform.Conflict("version_conflict", "Блокировка уже изменена"))
		return
	}
	if !b.Active {
		platform.Fail(w, platform.Conflict("block_inactive", "Блокировка уже снята"))
		return
	}
	before := b
	b.Active = false
	b.Version++
	if _, e = tx.Exec(r.Context(), "UPDATE resource_blocks SET active=false,version=version+1 WHERE id=$1", id); e != nil {
		platform.Fail(w, e)
		return
	}
	if e = audit(r.Context(), tx, platform.CurrentAdmin(r.Context()), "resource.unblock", id, before, b); e != nil {
		platform.Fail(w, e)
		return
	}
	if e = platform.Emit(r.Context(), tx, "resource.unblocked", dep, map[string]string{"department_id": dep, "block_id": id}); e != nil {
		platform.Fail(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, b)
}
func (s *Service) listBlocks(w http.ResponseWriter, r *http.Request) {
	dep := r.URL.Query().Get("department_id")
	if dep == "" {
		platform.Fail(w, platform.Bad("department_id обязателен"))
		return
	}
	limit, offset, e := platform.Page(r)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	var active *bool
	if value := r.URL.Query().Get("active"); value != "" {
		b, err := strconv.ParseBool(value)
		if err != nil {
			platform.Fail(w, platform.Bad("Некорректный active"))
			return
		}
		active = &b
	}
	var from, until *time.Time
	for _, field := range []struct {
		key    string
		target **time.Time
	}{{"from", &from}, {"until", &until}} {
		if value := r.URL.Query().Get(field.key); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				platform.Fail(w, platform.Bad("from/until: RFC3339"))
				return
			}
			*field.target = &parsed
		}
	}
	if from != nil && until != nil && !until.After(*from) {
		platform.Fail(w, platform.Bad("until должен быть позже from"))
		return
	}
	rows, e := s.DB.Query(r.Context(), `SELECT id::text,department_id,resource_ids,starts_at,ends_at,reason,version,active FROM resource_blocks
 WHERE department_id=$1 AND ($2::boolean IS NULL OR active=$2) AND ($3::timestamptz IS NULL OR ends_at>$3) AND ($4::timestamptz IS NULL OR starts_at<$4)
 AND ($5='' OR $5=ANY(resource_ids)) ORDER BY starts_at DESC,id DESC LIMIT $6 OFFSET $7`, dep, active, from, until, r.URL.Query().Get("resource_id"), limit, offset)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer rows.Close()
	items := []Block{}
	for rows.Next() {
		var b Block
		if e = rows.Scan(&b.ID, &b.DepartmentID, &b.ResourceIDs, &b.StartsAt, &b.EndsAt, &b.Reason, &b.Version, &b.Active); e != nil {
			platform.Fail(w, e)
			return
		}
		items = append(items, b)
	}
	if e = rows.Err(); e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset})
}
func page(r *http.Request) (int, int, error) {
	limit, offset := 50, 0
	var e error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, e = strconv.Atoi(v)
		if e != nil {
			return 0, 0, platform.Bad("Некорректный limit")
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, e = strconv.Atoi(v)
		if e != nil {
			return 0, 0, platform.Bad("Некорректный offset")
		}
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return 0, 0, platform.Bad("limit: 1..100, offset: 0..10000")
	}
	return limit, offset, nil
}
func (s *Service) adminOrders(w http.ResponseWriter, r *http.Request) {
	limit, offset, e := page(r)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	where := " WHERE true"
	args := []any{}
	add := func(field string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" AND %s=$%d", field, len(args))
	}
	if v := r.URL.Query().Get("department_id"); v != "" {
		add("department_id", v)
	}
	if v := r.URL.Query().Get("status"); v != "" {
		switch v {
		case "draft", "held", "confirmed", "released", "expired":
		default:
			platform.Fail(w, platform.Bad("Неизвестный статус"))
			return
		}
		add("(CASE WHEN status='held' AND expires_at<=clock_timestamp() THEN 'expired' ELSE status END)", v)
	}
	for _, key := range []string{"from", "until"} {
		if v := r.URL.Query().Get(key); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				platform.Fail(w, platform.Bad("from/until должны иметь формат RFC3339"))
				return
			}
			op := ">="
			if key == "until" {
				op = "<"
			}
			args = append(args, t)
			where += fmt.Sprintf(" AND starts_at %s $%d", op, len(args))
		}
	}
	args = append(args, limit, offset)
	rows, e := s.DB.Query(r.Context(), "SELECT id::text FROM orders"+where+fmt.Sprintf(" ORDER BY created_at DESC,id LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			platform.Fail(w, e)
			return
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		platform.Fail(w, e)
		return
	}
	items := make([]Order, len(ids))
	for i, id := range ids {
		items[i].ID = id
	}
	if e = s.paymentSummaries(r, items); e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset, "test_mode": true})
}
func (s *Service) auditList(w http.ResponseWriter, r *http.Request) {
	limit, offset, e := page(r)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	rows, e := s.DB.Query(r.Context(), "SELECT id,admin_id::text,action,entity_id,before_value,after_value,created_at FROM audit ORDER BY id DESC LIMIT $1 OFFSET $2", limit, offset)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var admin, action, entity string
		var before, after json.RawMessage
		var at time.Time
		if e = rows.Scan(&id, &admin, &action, &entity, &before, &after, &at); e != nil {
			platform.Fail(w, e)
			return
		}
		items = append(items, map[string]any{"id": id, "admin_id": admin, "action": action, "entity_id": entity, "before": before, "after": after, "created_at": at})
	}
	if e = rows.Err(); e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset})
}
