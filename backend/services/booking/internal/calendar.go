package booking

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

type AvailabilityItem struct {
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    time.Time  `json:"ends_at"`
	Gender    string     `json:"gender"`
	Resources []Resource `json:"resources"`
}

func durationQuery(r *http.Request, d Department) (int, error) {
	duration := d.MinDurationMinutes
	if value := r.URL.Query().Get("duration_minutes"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < d.MinDurationMinutes || n > 1440 || d.DurationStepMinutes < 1 || n%d.DurationStepMinutes != 0 {
			return 0, platform.Bad("Длительность не соответствует политике отделения")
		}
		duration = n
	}
	return duration, nil
}
func (s *Service) availabilityItems(ctx context.Context, d Department, date time.Time, duration int, now time.Time) ([]AvailabilityItem, error) {
	snap, err := s.loadAvailability(ctx, d, date, date.AddDate(0, 0, 1).Add(time.Duration(d.BufferMinutes)*time.Minute), now)
	if err != nil {
		return nil, err
	}
	items := []AvailabilityItem{}
	if len(snap.resources) == 0 {
		return items, nil
	}
	limited := false
	err = visitCandidates(ctx, d, date, duration, now, snap.resources[0].ID, func(sel Selection, gender string) {
		if (len(items)+1)*len(snap.resources) > 100000 {
			limited = true
			return
		}
		items = append(items, AvailabilityItem{sel.StartsAt, sel.EndsAt, gender, snap.states(sel.StartsAt, spanEnd(d, sel))})
	})
	if limited {
		return nil, platform.Err(503, "availability_limit", "Увеличьте длительность интервала для подробной доступности")
	}
	return items, err
}

func saleDay(d Department, date, now time.Time) bool {
	if date.Before(dayStart(now)) {
		return false
	}
	if d.Mode == "session" {
		return !date.After(dayStart(now.Add(7 * 24 * time.Hour)))
	}
	today := dayStart(now)
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, Location)
	limit := month.AddDate(0, 1, 0)
	if !today.Before(limit.AddDate(0, 0, -7)) {
		limit = limit.AddDate(0, 1, 0)
	}
	return date.Before(limit)
}
func (s *Service) calendar(w http.ResponseWriter, r *http.Request) {
	d, err := department(r.Context(), s.DB, chi.URLParam(r, "departmentID"), false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	from, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("from"), Location)
	if err != nil {
		platform.Fail(w, platform.Bad("from должен иметь формат YYYY-MM-DD"))
		return
	}
	to, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("to"), Location)
	if err != nil || to.Before(from) || to.Sub(from) > 30*24*time.Hour {
		platform.Fail(w, platform.Bad("Диапазон from..to должен содержать 1–31 день"))
		return
	}
	duration, err := durationQuery(r, d)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	now, err := nowDB(r.Context(), s.DB)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	snap, err := s.loadAvailability(r.Context(), d, from, to.AddDate(0, 0, 1).Add(time.Duration(d.BufferMinutes)*time.Minute), now)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	items := []map[string]any{}
	for date := from; !date.After(to); date = date.AddDate(0, 0, 1) {
		status := "available"
		availableSlots := 0
		free := map[string]bool{}
		genders := map[string]bool{}
		if !saleDay(d, date, now) {
			status = "out_of_sale"
		} else if d.Archived || len(slotsFor(d.Schedule, date)) == 0 {
			status = "closed"
		} else {
			if len(snap.resources) == 0 {
				status = "unavailable"
			} else {
				candidates := 0
				err = visitCandidates(r.Context(), d, date, duration, now, snap.resources[0].ID, func(sel Selection, gender string) {
					candidates++
					genders[gender] = true
					hasFree := false
					for _, resource := range snap.resources {
						if snap.state(resource.ID, sel.StartsAt, spanEnd(d, sel)) == "free" {
							free[resource.ID] = true
							hasFree = true
						}
					}
					if hasFree {
						availableSlots++
					}
				})
				if err != nil {
					platform.Fail(w, err)
					return
				}
				if candidates == 0 {
					status = "unavailable"
				} else if availableSlots == 0 {
					status = "sold_out"
				}
			}
		}
		names := []string{}
		for name := range genders {
			names = append(names, name)
		}
		sort.Strings(names)
		items = append(items, map[string]any{"date": date.Format("2006-01-02"), "status": status, "available_slots": availableSlots, "free_resources": len(free), "genders": names})
	}
	platform.JSON(w, 200, map[string]any{"department_id": d.ID, "from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "server_time": now, "items": items})
}
