package booking

import (
	"net/http"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func (s *Service) previewSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Schedule Schedule `json:"schedule" validate:"required"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if err := ValidateSchedule(in.Schedule); err != nil {
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
	d, err := department(r.Context(), tx, chi.URLParam(r, "departmentID"), true)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	d.Schedule = in.Schedule
	rows, err := tx.Query(r.Context(), "SELECT id::text,starts_at,ends_at,COALESCE(quote->>'gender','') FROM orders WHERE department_id=$1 AND ends_at>clock_timestamp() AND (status='confirmed' OR status='held' AND expires_at>clock_timestamp()) ORDER BY starts_at,id", d.ID)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	conflicts := []map[string]string{}
	conflictCount := 0
	for rows.Next() {
		var id string
		var sel Selection
		var originalGender string
		if err = rows.Scan(&id, &sel.StartsAt, &sel.EndsAt, &originalGender); err != nil {
			rows.Close()
			platform.Fail(w, err)
			return
		}
		gender, err := allowedSlot(d, sel)
		if err != nil || gender != originalGender {
			conflictCount++
			// Stream all rows to determine the result, retaining only a bounded sample.
			if len(conflicts) < 100 {
				conflicts = append(conflicts, map[string]string{"order_id": id, "reason": "Изменение нарушает время или назначение действующего заказа"})
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		platform.Fail(w, err)
		return
	}
	op.Commit(w, r, 200, map[string]any{"can_publish": conflictCount == 0, "current_version": d.ScheduleVersion, "conflicts": conflicts, "conflict_count": conflictCount, "conflicts_truncated": conflictCount > len(conflicts), "schedule": in.Schedule})
}
func (s *Service) previewTariffs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tariffs   Tariffs    `json:"tariffs" validate:"required"`
		Selection *Selection `json:"selection"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
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
	d, err := department(r.Context(), tx, chi.URLParam(r, "departmentID"), true)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = ValidateTariffs(in.Tariffs, d.Mode); err != nil {
		platform.Fail(w, err)
		return
	}
	result := map[string]any{"can_publish": true, "current_version": d.TariffVersion, "tariffs": in.Tariffs, "held_prices_unchanged": true}
	if in.Selection != nil {
		if in.Selection.DepartmentID != d.ID {
			platform.Fail(w, platform.Bad("Выбор относится к другому отделению"))
			return
		}
		now, err := nowDB(r.Context(), tx)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		d.Archived = false
		if err = ValidateSelection(d, *in.Selection, now, true); err != nil {
			platform.Fail(w, err)
			return
		}
		old, oldErr := buildQuote(r.Context(), tx, d, *in.Selection, now)
		if oldErr == nil {
			result["current_quote"] = old
		}
		d.Tariffs = in.Tariffs
		proposed, err := buildQuote(r.Context(), tx, d, *in.Selection, now)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		result["proposed_quote"] = proposed
		if oldErr == nil {
			result["price_difference_kopecks"] = proposed.Amount - old.Amount
		}
	}
	op.Commit(w, r, 200, result)
}
