package catalog

import (
	"errors"
	"net/http"
	"strings"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

func (s *Service) syncRetry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version int64  `json:"version" validate:"required,min=1"`
		Reason  string `json:"reason" validate:"required,min=1,max=500"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if !platform.SafeText(in.Reason, 1, 500, false) {
		platform.Fail(w, platform.Bad("Нужна причина повторной синхронизации"))
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
	actor := platform.CurrentAdmin(r.Context())
	op, err := platform.LoadOperation(r, tx, "admin:"+actor.ID, s.Config.InternalKey)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if op.Replay(w, r) {
		return
	}
	// The parent lock follows the same order as bath and department edits.
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
	if d.Version != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Отделение уже изменено"))
		return
	}
	before := d
	// Seeded departments predate booking_setup and already have booking-owned
	// configuration. Retrying metadata must preserve that configuration.
	if d.Setup != nil || d.Mode == "info" {
		payload := platform.CatalogDepartment{ID: d.ID, BathID: b.ID, BathName: b.Name, BathAddress: b.Address, Name: d.Name, Mode: d.Mode, Active: d.Active && b.Active, Version: d.Version + 1, Setup: d.Setup}
		if err = s.validateSetup(r, payload); err != nil {
			platform.Fail(w, err)
			return
		}
	}
	d, err = scanDepartment(tx.QueryRow(r.Context(), "UPDATE departments SET version=version+1,sync_status='pending',sync_error='' WHERE id=$1 RETURNING "+deptColumns, id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	// A fresh version/event also recovers a lost ACK or an already-inboxed error.
	// Reusing the original event ID would be discarded by consumer deduplication.
	if err = emitDepartment(r.Context(), tx, b, d); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = change(r.Context(), tx, actor.ID, "department", id, "department.sync_retry", before, map[string]any{"department": d, "reason": in.Reason}); err != nil {
		platform.Fail(w, err)
		return
	}
	op.AfterCommit = func() { s.clearCache(r.Context()) }
	op.Commit(w, r, 202, d)
}
