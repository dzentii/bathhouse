package booking

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"banya.local/backend/internal/platform"
)

func minute(v string) (int, error) {
	if v == "24:00" {
		return 1440, nil
	}
	if len(v) != 5 || v[2] != ':' {
		return 0, fmt.Errorf("invalid time")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if v[i] < '0' || v[i] > '9' {
			return 0, fmt.Errorf("invalid time")
		}
	}
	h, e := strconv.Atoi(v[:2])
	if e != nil {
		return 0, e
	}
	m, e := strconv.Atoi(v[3:])
	if e != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid time")
	}
	return h*60 + m, nil
}
func dayStart(t time.Time) time.Time {
	t = t.In(Location)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Location)
}
func slotsFor(s Schedule, t time.Time) []Slot {
	date := t.In(Location).Format("2006-01-02")
	if a, ok := s.Exceptions[date]; ok {
		return a
	}
	return s.Week[strconv.Itoa(int(t.In(Location).Weekday()))]
}
func dayType(s Schedule, t time.Time) string {
	if k := s.Calendar[t.In(Location).Format("2006-01-02")]; k != "" {
		return k
	}
	w := t.In(Location).Weekday()
	if w == time.Saturday || w == time.Sunday {
		return "weekend"
	}
	return "weekday"
}
func ValidateSchedule(s Schedule) error {
	if s.Week == nil || len(s.Week) > 7 || len(s.Exceptions) > 800 || len(s.Calendar) > 800 {
		return platform.Bad("Недопустимый размер расписания")
	}
	check := func(slots []Slot) error {
		if len(slots) > 48 {
			return platform.Bad("Слишком много интервалов")
		}
		copySlots := append([]Slot(nil), slots...)
		sort.Slice(copySlots, func(i, j int) bool { return copySlots[i].Start < copySlots[j].Start })
		previous := -1
		for _, a := range copySlots {
			start, e := minute(a.Start)
			if e != nil {
				return platform.Bad("Неверное начало интервала")
			}
			end, e := minute(a.End)
			if e != nil || end <= start || start < previous {
				return platform.Bad("Интервалы расписания пересекаются или некорректны")
			}
			if a.Gender != "male" && a.Gender != "female" && a.Gender != "mixed" {
				return platform.Bad("Неизвестное назначение дня")
			}
			previous = end
		}
		return nil
	}
	for key, slots := range s.Week {
		n, e := strconv.Atoi(key)
		if e != nil || n < 0 || n > 6 || strconv.Itoa(n) != key {
			return platform.Bad("День недели должен быть 0..6")
		}
		if e = check(slots); e != nil {
			return e
		}
	}
	for key, slots := range s.Exceptions {
		if _, e := time.Parse("2006-01-02", key); e != nil {
			return platform.Bad("Неверная дата исключения")
		}
		if e := check(slots); e != nil {
			return e
		}
	}
	for key, kind := range s.Calendar {
		if _, e := time.Parse("2006-01-02", key); e != nil {
			return platform.Bad("Неверная дата календаря")
		}
		if kind != "weekday" && kind != "weekend" && kind != "holiday" {
			return platform.Bad("Неверный тип календарного дня")
		}
	}
	return nil
}
func allowedSlot(d Department, s Selection) (string, error) {
	start, end := s.StartsAt.In(Location), s.EndsAt.In(Location)
	base := dayStart(start)
	for _, slot := range slotsFor(d.Schedule, start) {
		a, e := minute(slot.Start)
		if e != nil {
			return "", e
		}
		b, e := minute(slot.End)
		if e != nil {
			return "", e
		}
		open, close := base.Add(time.Duration(a)*time.Minute), base.Add(time.Duration(b)*time.Minute)
		if d.Mode == "session" && start.Equal(open) && end.Equal(close) {
			return slot.Gender, nil
		}
		if d.Mode == "sauna" && d.StartStepMinutes > 0 && d.DurationStepMinutes > 0 && d.MinDurationMinutes > 0 && !start.Before(open) && !end.After(close) && start.Sub(open)%(time.Duration(d.StartStepMinutes)*time.Minute) == 0 && end.Sub(start) >= time.Duration(d.MinDurationMinutes)*time.Minute && end.Sub(start)%(time.Duration(d.DurationStepMinutes)*time.Minute) == 0 {
			return slot.Gender, nil
		}
	}
	return "", platform.Conflict("schedule_conflict", "Выбранное время отсутствует в опубликованном расписании")
}
func ValidateSelection(d Department, s Selection, now time.Time, checkHorizon bool) error {
	if d.Archived {
		return platform.Conflict("sales_closed", "Отделение архивировано; новое оформление закрыто")
	}
	if !s.EndsAt.After(s.StartsAt) || s.EndsAt.Sub(s.StartsAt) > 24*time.Hour || s.StartsAt.Second() != 0 || s.EndsAt.Second() != 0 || s.StartsAt.Nanosecond() != 0 || s.EndsAt.Nanosecond() != 0 {
		return platform.Bad("Некорректный интервал посещения")
	}
	if checkHorizon {
		if !s.StartsAt.After(now) {
			return platform.Conflict("sales_closed", "Начало посещения уже наступило")
		}
		if d.Mode == "session" && s.StartsAt.After(now.Add(7*24*time.Hour)) {
			return platform.Bad("Сеансы доступны не более чем за 7 суток")
		}
		if d.Mode == "sauna" {
			today := dayStart(now)
			month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, Location)
			next := month.AddDate(0, 1, 0)
			limit := next
			if !today.Before(next.AddDate(0, 0, -7)) {
				limit = next.AddDate(0, 1, 0)
			}
			if !s.StartsAt.Before(limit) {
				return platform.Bad("Этот месяц ещё не открыт для записи")
			}
		}
	}
	if len(s.ResourceIDs) < 1 || len(s.ResourceIDs) > 100 || len(s.Guests) < 1 || len(s.Guests) > 100 {
		return platform.Bad("Пустой или слишком большой состав заказа")
	}
	seen := map[string]bool{}
	for _, id := range s.ResourceIDs {
		if seen[id] {
			return platform.Bad("Ресурс указан дважды")
		}
		seen[id] = true
	}
	total, withSeat, adults := 0, 0, 0
	for _, g := range s.Guests {
		if g.Age < 0 || g.Age > 120 || g.Count < 1 || g.Count > 100 {
			return platform.Bad("Недопустимые возраст или количество гостей")
		}
		total += g.Count
		if g.Age >= 8 {
			withSeat += g.Count
		}
		if g.Age >= 18 {
			adults += g.Count
		}
	}
	if total > d.MaxGuests || adults == 0 {
		return platform.Bad("Превышена вместимость или отсутствует совершеннолетний сопровождающий")
	}
	if d.Mode == "session" && withSeat != len(s.ResourceIDs) {
		return platform.Bad("Каждому гостю от 8 лет требуется отдельное место")
	}
	if d.Mode == "sauna" && len(s.ResourceIDs) != 1 {
		return platform.Bad("Сауна оформляется целиком одним ресурсом")
	}
	_, err := allowedSlot(d, s)
	return err
}
