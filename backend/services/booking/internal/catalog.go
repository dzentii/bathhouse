package booking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
)

func setupDepartment(v platform.CatalogDepartment) (Department, error) {
	d := Department{ID: v.ID, BathID: v.BathID, BathName: v.BathName, BathAddress: v.BathAddress, Name: v.Name, Mode: v.Mode, Archived: !v.Active, CatalogVersion: v.Version, ScheduleVersion: 1, TariffVersion: 1, PolicyVersion: 1, SchemeVersion: 1}
	if v.Mode == "info" {
		if v.Setup != nil {
			return d, platform.Bad("Справочному отделению не задают онлайн-ресурсы")
		}
		return d, nil
	}
	if v.Mode != "session" && v.Mode != "sauna" || v.Setup == nil {
		return d, platform.Bad("Нужны режим session/sauna и начальная конфигурация")
	}
	a := v.Setup
	d.BaseGuests = a.BaseGuests
	d.MaxGuests = a.MaxGuests
	d.MinDurationMinutes = a.MinDurationMinutes
	d.DurationStepMinutes = a.DurationStepMinutes
	d.StartStepMinutes = a.StartStepMinutes
	d.BufferMinutes = a.BufferMinutes
	if err := validatePolicy(d); err != nil {
		return d, err
	}
	for _, item := range []struct {
		raw    json.RawMessage
		target any
	}{{a.Schedule, &d.Schedule}, {a.Tariffs, &d.Tariffs}} {
		decoder := json.NewDecoder(bytes.NewReader(item.raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(item.target); err != nil {
			return d, platform.Bad("Некорректные поля расписания или тарифов")
		}
	}
	if err := ValidateSchedule(d.Schedule); err != nil {
		return d, err
	}
	if err := ValidateTariffs(d.Tariffs, d.Mode); err != nil {
		return d, err
	}
	if len(a.Resources) < 1 || len(a.Resources) > 500 || d.Mode == "sauna" && len(a.Resources) != 1 {
		return d, platform.Bad("Нужны 1–500 мест либо один ресурс сауны")
	}
	labels := map[string]bool{}
	ids := map[string]bool{}
	for _, r := range a.Resources {
		r.Label = strings.TrimSpace(r.Label)
		if uuid.Validate(r.ID) != nil || !platform.SafeText(strings.TrimSpace(r.Label), 1, 80, false) || labels[r.Label] || ids[r.ID] || r.X < 0 || r.X > 1000 || r.Y < 0 || r.Y > 1000 {
			return d, platform.Bad("Некорректные или повторные ресурсы схемы")
		}
		labels[r.Label] = true
		ids[r.ID] = true
	}
	return d, nil
}
func (s *Service) validateCatalog(w http.ResponseWriter, r *http.Request) {
	var v platform.CatalogDepartment
	if err := platform.Decode(w, r, &v); err != nil {
		platform.Fail(w, err)
		return
	}
	if _, err := setupDepartment(v); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]bool{"valid": true})
}
func (s *Service) applyCatalog(ctx context.Context, e platform.Event) error {
	var v platform.CatalogDepartment
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return err
	}
	if !platform.SafeText(v.ID, 1, 80, false) || v.Version < 1 || v.BathID < 1 {
		return errors.New("invalid catalog event")
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
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "catalog-projection:"+v.ID); err != nil {
		return err
	}
	ack := platform.CatalogApplied{DepartmentID: v.ID, Version: v.Version}
	d, lookupErr := department(ctx, tx, v.ID, true)
	var apiErr *platform.APIError
	missing := errors.As(lookupErr, &apiErr) && apiErr.Status == 404
	if lookupErr != nil && !missing {
		return lookupErr
	}
	switch {
	case v.Mode == "info":
		if !missing {
			ack.Error = "Нельзя изменить способ посещения действующего онлайн-отделения"
		}
	case !missing && d.Mode != v.Mode:
		ack.Error = "Нельзя изменить способ посещения действующего отделения"
	case !missing && v.Version <= d.CatalogVersion:
		// A newer metadata version already won; never roll it back on replay.
	case missing:
		var proposed Department
		proposed, err = setupDepartment(v)
		if err != nil {
			ack.Error = "Начальная конфигурация отделения не прошла проверку"
			break
		}
		_, err = tx.Exec(ctx, `INSERT INTO departments(id,bath_id,bath_name,bath_address,name,mode,base_guests,max_guests,schedule,tariffs,min_duration_minutes,duration_step_minutes,start_step_minutes,buffer_minutes,archived,catalog_version)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, v.ID, v.BathID, v.BathName, v.BathAddress, v.Name, v.Mode, proposed.BaseGuests, proposed.MaxGuests, bytesJSON(proposed.Schedule), bytesJSON(proposed.Tariffs), proposed.MinDurationMinutes, proposed.DurationStepMinutes, proposed.StartStepMinutes, proposed.BufferMinutes, !v.Active, v.Version)
		if err != nil {
			return err
		}
		for _, r := range v.Setup.Resources {
			if _, err = tx.Exec(ctx, "INSERT INTO resources(id,department_id,label,x,y) VALUES($1,$2,$3,$4,$5)", r.ID, v.ID, strings.TrimSpace(r.Label), r.X, r.Y); err != nil {
				return err
			}
		}
	default:
		_, err = tx.Exec(ctx, "UPDATE departments SET bath_id=$2,bath_name=$3,bath_address=$4,name=$5,archived=$6,catalog_version=$7 WHERE id=$1", v.ID, v.BathID, v.BathName, v.BathAddress, v.Name, !v.Active, v.Version)
		if err != nil {
			return err
		}
	}
	if err = platform.Emit(ctx, tx, "booking.catalog_applied", v.ID, ack); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.invalidate(ctx, v.ID)
	return nil
}
