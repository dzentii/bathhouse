package booking

import (
	"fmt"
	"slices"
	"time"

	"banya.local/backend/internal/platform"
)

func ValidateTariffs(t Tariffs, mode string) error {
	if len(t.Rules) == 0 || len(t.Rules) > 200 {
		return platform.Bad("Требуется от 1 до 200 тарифных правил")
	}
	ids := map[string]bool{}
	for i, r := range t.Rules {
		a, e1 := time.Parse("2006-01-02", r.ValidFrom)
		b, e2 := time.Parse("2006-01-02", r.ValidUntil)
		if e1 != nil || e2 != nil || !b.After(a) || r.ID == "" || len(r.ID) > 100 || ids[r.ID] || r.StartMinute < 0 || r.EndMinute > 1440 || r.EndMinute <= r.StartMinute || len(r.DayTypes) == 0 {
			return platform.Bad("Некорректные границы или идентификатор тарифного правила")
		}
		ids[r.ID] = true
		if r.Adult < 0 || r.Child < 0 || r.Hourly < 0 || r.ExtraHourly < 0 || r.Adult > 100000000 || r.Child > 100000000 || r.Hourly > 100000000 || r.ExtraHourly > 100000000 {
			return platform.Bad("Недопустимая стоимость")
		}
		if mode == "sauna" && (r.Hourly <= 0 || r.DurationMinutes != 0) {
			return platform.Bad("Сауне требуется положительный почасовой тариф")
		}
		if mode == "session" && (r.Adult <= 0 || r.DurationMinutes < 1 || r.DurationMinutes > 1440) {
			return platform.Bad("Сеансу требуется стоимость и длительность")
		}
		seen := map[string]bool{}
		for _, k := range r.DayTypes {
			if (k != "weekday" && k != "weekend" && k != "holiday") || seen[k] {
				return platform.Bad("Неизвестный или повторный тип тарифного дня")
			}
			seen[k] = true
		}
		for _, p := range t.Rules[:i] {
			if r.DurationMinutes != p.DurationMinutes || r.ValidFrom >= p.ValidUntil || p.ValidFrom >= r.ValidUntil || r.StartMinute >= p.EndMinute || p.StartMinute >= r.EndMinute {
				continue
			}
			for _, k := range r.DayTypes {
				if slices.Contains(p.DayTypes, k) {
					return platform.Conflict("tariff_overlap", "Тарифные правила пересекаются")
				}
			}
		}
	}
	return nil
}
func tariffAt(d Department, t time.Time, duration int) (Tariff, error) {
	t = t.In(Location)
	date := t.Format("2006-01-02")
	m := t.Hour()*60 + t.Minute()
	kind := dayType(d.Schedule, t)
	var found Tariff
	matches := 0
	for _, r := range d.Tariffs.Rules {
		if date >= r.ValidFrom && date < r.ValidUntil && m >= r.StartMinute && m < r.EndMinute && r.DurationMinutes == duration && slices.Contains(r.DayTypes, kind) {
			found = r
			matches++
		}
	}
	if matches != 1 {
		return Tariff{}, platform.Conflict("tariff_unavailable", "Для выбранного времени нет однозначного тарифа")
	}
	return found, nil
}
func Calculate(d Department, s Selection, resources []Resource) (Quote, error) {
	gender, err := allowedSlot(d, s)
	if err != nil {
		return Quote{}, err
	}
	q := Quote{DepartmentID: d.ID, BathID: d.BathID, BathName: d.BathName, BathAddress: d.BathAddress, DepartmentName: d.Name, Mode: d.Mode, StartsAt: s.StartsAt.UTC(), EndsAt: s.EndsAt.UTC(), Resources: resources, Guests: s.Guests, Gender: gender, Lines: []PriceLine{}, Currency: "RUB", ScheduleVersion: d.ScheduleVersion, TariffVersion: d.TariffVersion, Preliminary: true}
	numerator := int64(0)
	add := func(desc string, r Tariff, minutes, count int, rate int64) {
		if count == 0 {
			return
		}
		n := rate * int64(minutes) * int64(count)
		q.Lines = append(q.Lines, PriceLine{desc, r.ID, minutes, count, rate, n, 60})
		numerator += n
	}
	if d.Mode == "sauna" {
		billable := 0
		for _, g := range s.Guests {
			if g.Age >= 13 {
				billable += g.Count
			}
		}
		extra := max(0, billable-d.BaseGuests)
		for t := s.StartsAt.In(Location); t.Before(s.EndsAt); {
			r, e := tariffAt(d, t, 0)
			if e != nil {
				return q, e
			}
			boundary := dayStart(t).Add(time.Duration(r.EndMinute) * time.Minute)
			if s.EndsAt.Before(boundary) {
				boundary = s.EndsAt
			}
			minutes := int(boundary.Sub(t) / time.Minute)
			if minutes <= 0 {
				return q, fmt.Errorf("non advancing tariff boundary")
			}
			add("Аренда сауны целиком", r, minutes, 1, r.Hourly)
			add("Дополнительные гости от 13 лет", r, minutes, extra, r.ExtraHourly)
			t = boundary
		}
	} else {
		r, e := tariffAt(d, s.StartsAt, int(s.EndsAt.Sub(s.StartsAt)/time.Minute))
		if e != nil {
			return q, e
		}
		adults, children, free := 0, 0, 0
		for _, g := range s.Guests {
			switch {
			case g.Age >= 8:
				adults += g.Count
			case g.Age >= 3:
				children += g.Count
			default:
				free += g.Count
			}
		}
		add("Билет с отдельным местом", r, 60, adults, r.Adult)
		add("Ребёнок 3–7 лет без места", r, 60, children, r.Child)
		add("Ребёнок до 3 лет без места", r, 60, free, 0)
	}
	q.Amount = (numerator + 30) / 60
	if q.Amount <= 0 {
		return q, platform.Conflict("tariff_unavailable", "Цена заказа должна быть положительной")
	}
	q.PriceHash = platform.Hash(string(bytesJSON(q)))
	return q, nil
}
