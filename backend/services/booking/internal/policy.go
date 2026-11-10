package booking

import (
	"net/http"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

type BookingPolicy struct {
	OnlineSalesEnabled  bool       `json:"online_sales_enabled"`
	SalesPausedUntil    *time.Time `json:"sales_paused_until"`
	SalesPauseReason    string     `json:"sales_pause_reason"`
	Version             int64      `json:"version"`
	BaseGuests          int        `json:"base_guests"`
	MaxGuests           int        `json:"max_guests"`
	MinDurationMinutes  int        `json:"min_duration_minutes"`
	DurationStepMinutes int        `json:"duration_step_minutes"`
	StartStepMinutes    int        `json:"start_step_minutes"`
	BufferMinutes       int        `json:"buffer_minutes"`
}

func policy(d Department) BookingPolicy {
	now := d.ServerTime
	if now.IsZero() {
		now = time.Now()
	}
	d = salesView(d, now)
	return BookingPolicy{Version: d.PolicyVersion, BaseGuests: d.BaseGuests, MaxGuests: d.MaxGuests, MinDurationMinutes: d.MinDurationMinutes, DurationStepMinutes: d.DurationStepMinutes, StartStepMinutes: d.StartStepMinutes, BufferMinutes: d.BufferMinutes, OnlineSalesEnabled: d.OnlineSalesEnabled, SalesPausedUntil: d.SalesPausedUntil, SalesPauseReason: d.SalesPauseReason}
}
func validatePolicy(d Department) error {
	if d.BaseGuests < 1 || d.MaxGuests < d.BaseGuests || d.MaxGuests > 1000 || d.MinDurationMinutes < 1 || d.MinDurationMinutes > 1440 || d.DurationStepMinutes < 1 || d.DurationStepMinutes > 1440 || d.StartStepMinutes < 1 || d.StartStepMinutes > 1440 || d.BufferMinutes < 0 || d.BufferMinutes > 240 {
		return platform.Bad("Недопустимые вместимость, шаги времени или перерыв")
	}
	if d.Mode == "sauna" && d.MinDurationMinutes%d.DurationStepMinutes != 0 {
		return platform.Bad("Минимальная длительность должна быть кратна шагу длительности")
	}
	if d.Mode == "session" && d.BufferMinutes != 0 {
		return platform.Bad("Для сеансов перерывы задаются расписанием; buffer_minutes должен быть 0")
	}
	return nil
}
func (s *Service) getPolicy(w http.ResponseWriter, r *http.Request) {
	d, err := department(r.Context(), s.DB, chi.URLParam(r, "departmentID"), false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, policy(d))
}
func (s *Service) patchPolicy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		salesPatch
		Version      int64 `json:"version" validate:"required,min=1"`
		BaseGuests   *int  `json:"base_guests"`
		MaxGuests    *int  `json:"max_guests"`
		MinDuration  *int  `json:"min_duration_minutes"`
		DurationStep *int  `json:"duration_step_minutes"`
		StartStep    *int  `json:"start_step_minutes"`
		Buffer       *int  `json:"buffer_minutes"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if in.BaseGuests == nil && in.MaxGuests == nil && in.MinDuration == nil && in.DurationStep == nil && in.StartStep == nil && in.Buffer == nil && !in.present() {
		platform.Fail(w, platform.Bad("Не заданы изменения"))
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
	if d.PolicyVersion != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Политика бронирования уже изменена"))
		return
	}
	if err = expireDepartment(r.Context(), tx, d.ID); err != nil {
		platform.Fail(w, err)
		return
	}
	d.ServerTime, err = nowDB(r.Context(), tx)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	before := policy(d)
	proposed, err := applySalesPatch(d, in.salesPatch, d.ServerTime)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	for _, field := range []struct {
		value  *int
		target *int
	}{{in.BaseGuests, &proposed.BaseGuests}, {in.MaxGuests, &proposed.MaxGuests}, {in.MinDuration, &proposed.MinDurationMinutes}, {in.DurationStep, &proposed.DurationStepMinutes}, {in.StartStep, &proposed.StartStepMinutes}, {in.Buffer, &proposed.BufferMinutes}} {
		if field.value != nil {
			*field.target = *field.value
		}
	}
	if err = validatePolicy(proposed); err != nil {
		platform.Fail(w, err)
		return
	}
	if proposed.BaseGuests != d.BaseGuests || proposed.MaxGuests != d.MaxGuests || proposed.MinDurationMinutes != d.MinDurationMinutes || proposed.DurationStepMinutes != d.DurationStepMinutes || proposed.StartStepMinutes != d.StartStepMinutes || proposed.BufferMinutes != d.BufferMinutes {
		rows, err := tx.Query(r.Context(), "SELECT selection FROM orders WHERE department_id=$1 AND status IN ('held','confirmed') AND ends_at>clock_timestamp()", d.ID)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		validation := proposed
		validation.Archived = false
		for rows.Next() {
			var sel Selection
			if err = rows.Scan(&sel); err != nil {
				rows.Close()
				platform.Fail(w, err)
				return
			}
			if proposed.BufferMinutes != d.BufferMinutes || ValidateSelection(validation, sel, sel.StartsAt, false) != nil {
				rows.Close()
				platform.Fail(w, platform.Conflict("existing_order_conflict", "Правило нарушает действующее удержание или подтверждённый заказ"))
				return
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			platform.Fail(w, err)
			return
		}
	}
	_, err = tx.Exec(r.Context(), "UPDATE departments SET base_guests=$2,max_guests=$3,min_duration_minutes=$4,duration_step_minutes=$5,start_step_minutes=$6,buffer_minutes=$7,online_sales_enabled=$8,sales_paused_until=$9,sales_pause_reason=$10,policy_version=policy_version+1 WHERE id=$1", d.ID, proposed.BaseGuests, proposed.MaxGuests, proposed.MinDurationMinutes, proposed.DurationStepMinutes, proposed.StartStepMinutes, proposed.BufferMinutes, proposed.OnlineSalesEnabled, proposed.SalesPausedUntil, proposed.SalesPauseReason)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	proposed.PolicyVersion++
	after := policy(proposed)
	if err = audit(r.Context(), tx, platform.CurrentAdmin(r.Context()), "policy.update", d.ID, before, after); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = platform.Emit(r.Context(), tx, "department.changed", d.ID, map[string]string{"department_id": d.ID}); err != nil {
		platform.Fail(w, err)
		return
	}
	op.AfterCommit = func() { s.invalidate(r.Context(), d.ID) }
	op.Commit(w, r, 200, after)
}
