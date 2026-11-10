package booking

import (
	"net/http"
	"strings"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) patchBlock(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version  int64      `json:"version" validate:"required,min=1"`
		StartsAt *time.Time `json:"starts_at"`
		EndsAt   *time.Time `json:"ends_at"`
		Reason   *string    `json:"reason"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	id := chi.URLParam(r, "blockID")
	if uuid.Validate(id) != nil || in.StartsAt == nil && in.EndsAt == nil && in.Reason == nil {
		platform.Fail(w, platform.Bad("Нужны корректная блокировка и изменения"))
		return
	}
	var dep string
	if err := s.DB.QueryRow(r.Context(), "SELECT department_id FROM resource_blocks WHERE id=$1", id).Scan(&dep); err != nil {
		if err == pgx.ErrNoRows {
			err = platform.Err(404, "not_found", "Блокировка не найдена")
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
	op, err := platform.LoadOperation(r, tx, "admin:"+platform.CurrentAdmin(r.Context()).ID, s.Config.InternalKey)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if op.Replay(w, r) {
		return
	}
	if _, err = department(r.Context(), tx, dep, true); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = expireDepartment(r.Context(), tx, dep); err != nil {
		platform.Fail(w, err)
		return
	}
	var b Block
	err = tx.QueryRow(r.Context(), "SELECT id::text,department_id,resource_ids,starts_at,ends_at,reason,version,active FROM resource_blocks WHERE id=$1 FOR UPDATE", id).Scan(&b.ID, &b.DepartmentID, &b.ResourceIDs, &b.StartsAt, &b.EndsAt, &b.Reason, &b.Version, &b.Active)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	before := b
	if b.Version != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Блокировка уже изменена"))
		return
	}
	if !b.Active {
		platform.Fail(w, platform.Conflict("block_inactive", "Снятая блокировка не редактируется"))
		return
	}
	if in.StartsAt != nil {
		b.StartsAt = *in.StartsAt
	}
	if in.EndsAt != nil {
		b.EndsAt = *in.EndsAt
	}
	if in.Reason != nil {
		b.Reason = strings.TrimSpace(*in.Reason)
	}
	if b.StartsAt.IsZero() || !b.EndsAt.After(b.StartsAt) || b.EndsAt.Sub(b.StartsAt) > 366*24*time.Hour || !platform.SafeText(b.Reason, 1, 300, false) {
		platform.Fail(w, platform.Bad("Недопустимые период или причина блокировки"))
		return
	}
	var conflict bool
	err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM allocations WHERE active AND resource_id=ANY($1) AND span && tstzrange($2,$3,'[)'))", b.ResourceIDs, b.StartsAt, b.EndsAt).Scan(&conflict)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if conflict {
		platform.Fail(w, platform.Conflict("existing_order_conflict", "Период пересекает действующее оформление"))
		return
	}
	err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM resource_blocks WHERE active AND department_id=$1 AND id<>$2 AND resource_ids && $3::text[] AND tstzrange(starts_at,ends_at,'[)') && tstzrange($4,$5,'[)'))", dep, id, b.ResourceIDs, b.StartsAt, b.EndsAt).Scan(&conflict)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if conflict {
		platform.Fail(w, platform.Conflict("block_overlap", "Период пересекает другую блокировку"))
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE resource_blocks SET starts_at=$2,ends_at=$3,reason=$4,version=version+1 WHERE id=$1", id, b.StartsAt, b.EndsAt, b.Reason); err != nil {
		platform.Fail(w, err)
		return
	}
	b.Version++
	if err = audit(r.Context(), tx, platform.CurrentAdmin(r.Context()), "resource.block.update", id, before, b); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = platform.Emit(r.Context(), tx, "resource.block_updated", dep, map[string]string{"department_id": dep, "block_id": id}); err != nil {
		platform.Fail(w, err)
		return
	}
	op.Commit(w, r, 200, b)
}
