package booking

import (
	"net/http"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BlockRelease explicitly names both the resources and the half-open interval to free.
// Omitting a boundary must never silently expand an administrative operation.
type BlockRelease struct {
	Version     int64     `json:"version" validate:"required,min=1"`
	ResourceIDs []string  `json:"resource_ids" validate:"required,min=1,max=100,dive,required,max=100"`
	StartsAt    time.Time `json:"starts_at" validate:"required"`
	EndsAt      time.Time `json:"ends_at" validate:"required"`
}

type BlockReleaseResult struct {
	Original  Block        `json:"original"`
	Remaining []Block      `json:"remaining"`
	Released  BlockRelease `json:"released"`
}

// remainingBlocks subtracts a resource/time rectangle without widening the release.
// It produces at most three disjoint rectangles and does not modify the source.
func remainingBlocks(b Block, in BlockRelease) ([]Block, error) {
	if !in.StartsAt.Equal(in.StartsAt.Truncate(time.Microsecond)) || !in.EndsAt.Equal(in.EndsAt.Truncate(time.Microsecond)) {
		return nil, platform.Bad("Точность времени не должна превышать микросекунды")
	}
	if in.StartsAt.IsZero() || !in.EndsAt.After(in.StartsAt) || in.StartsAt.Before(b.StartsAt) || in.EndsAt.After(b.EndsAt) || len(in.ResourceIDs) == 0 || len(in.ResourceIDs) > 100 {
		return nil, platform.Bad("Период снятия должен находиться внутри блокировки")
	}
	selected := make(map[string]bool, len(in.ResourceIDs))
	for _, id := range in.ResourceIDs {
		if selected[id] {
			return nil, platform.Bad("Ресурс указан дважды")
		}
		selected[id] = true
	}
	untouched, affected := []string{}, []string{}
	for _, id := range b.ResourceIDs {
		if selected[id] {
			affected = append(affected, id)
		} else {
			untouched = append(untouched, id)
		}
	}
	if len(affected) != len(selected) {
		return nil, platform.Bad("Выбранные ресурсы не входят в блокировку")
	}
	remaining := make([]Block, 0, 3)
	add := func(ids []string, from, until time.Time) {
		if len(ids) == 0 || !until.After(from) {
			return
		}
		part := b
		part.ID, part.Version, part.Active = uuid.NewString(), 1, true
		part.ResourceIDs = append([]string(nil), ids...)
		part.StartsAt, part.EndsAt = from, until
		remaining = append(remaining, part)
	}
	add(untouched, b.StartsAt, b.EndsAt)
	add(affected, b.StartsAt, in.StartsAt)
	add(affected, in.EndsAt, b.EndsAt)
	return remaining, nil
}

func (s *Service) releaseBlock(w http.ResponseWriter, r *http.Request) {
	var in BlockRelease
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	parsedID, err := uuid.Parse(chi.URLParam(r, "blockID"))
	if err != nil {
		platform.Fail(w, platform.Bad("Некорректный blockID"))
		return
	}
	id := parsedID.String()
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
	// Same lock as holds and all other block changes: no concurrent allocation can
	// observe the old block disabled before its unaffected pieces have been saved.
	if _, err = department(r.Context(), tx, dep, true); err != nil {
		platform.Fail(w, err)
		return
	}
	var before Block
	if err = tx.QueryRow(r.Context(), "SELECT id::text,department_id,resource_ids,starts_at,ends_at,reason,version,active FROM resource_blocks WHERE id=$1 FOR UPDATE", id).Scan(&before.ID, &before.DepartmentID, &before.ResourceIDs, &before.StartsAt, &before.EndsAt, &before.Reason, &before.Version, &before.Active); err != nil {
		platform.Fail(w, err)
		return
	}
	if before.Version != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Блокировка уже изменена"))
		return
	}
	if !before.Active {
		platform.Fail(w, platform.Conflict("block_inactive", "Блокировка уже снята"))
		return
	}
	remaining, err := remainingBlocks(before, in)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	original := before
	original.Active, original.Version = false, before.Version+1
	if _, err = tx.Exec(r.Context(), "UPDATE resource_blocks SET active=false,version=version+1 WHERE id=$1", id); err != nil {
		platform.Fail(w, err)
		return
	}
	for _, b := range remaining {
		if _, err = tx.Exec(r.Context(), "INSERT INTO resource_blocks(id,department_id,resource_ids,starts_at,ends_at,reason) VALUES($1,$2,$3,$4,$5,$6)", b.ID, b.DepartmentID, b.ResourceIDs, b.StartsAt, b.EndsAt, b.Reason); err != nil {
			platform.Fail(w, err)
			return
		}
		if err = platform.Emit(r.Context(), tx, "resource.blocked", dep, map[string]string{"department_id": dep, "block_id": b.ID}); err != nil {
			platform.Fail(w, err)
			return
		}
	}
	result := BlockReleaseResult{Original: original, Remaining: remaining, Released: in}
	if err = audit(r.Context(), tx, platform.CurrentAdmin(r.Context()), "resource.block.release", id, before, result); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = platform.Emit(r.Context(), tx, "resource.unblocked", dep, map[string]string{"department_id": dep, "block_id": id}); err != nil {
		platform.Fail(w, err)
		return
	}
	op.Commit(w, r, 200, result)
}
