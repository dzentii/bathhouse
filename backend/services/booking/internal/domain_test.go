package booking

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMinuteRejectsSignedTime(t *testing.T) {
	for _, value := range []string{"+1:00", "-0:00", "01:+1", "01:-0", "01:60", "24:01"} {
		_, err := minute(value)
		require.Error(t, err, value)
	}
	for value, want := range map[string]int{"00:00": 0, "01:09": 69, "23:59": 1439, "24:00": 1440} {
		got, err := minute(value)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func sauna() Department {
	return Department{ID: "sauna", Mode: "sauna", BaseGuests: 5, MaxGuests: 10, MinDurationMinutes: 60, DurationStepMinutes: 60, StartStepMinutes: 30, BufferMinutes: 30, Schedule: Schedule{Week: map[string][]Slot{"3": {{"09:00", "23:00", "mixed"}}}}, Tariffs: Tariffs{Rules: []Tariff{
		{ID: "day", ValidFrom: "2026-09-30", ValidUntil: "2100-01-01", DayTypes: []string{"weekday"}, StartMinute: 0, EndMinute: 960, Hourly: 150000, ExtraHourly: 30000},
		{ID: "evening", ValidFrom: "2026-09-30", ValidUntil: "2100-01-01", DayTypes: []string{"weekday"}, StartMinute: 960, EndMinute: 1440, Hourly: 190000, ExtraHourly: 38000},
	}}}
}
func TestSaunaUsesResourcePolicy(t *testing.T) {
	d := sauna()
	s := visit()
	d.MinDurationMinutes = 120
	require.Error(t, ValidateSelection(d, s, local("2026-11-10 00:00"), true))
	s.EndsAt = s.StartsAt.Add(2 * time.Hour)
	require.NoError(t, ValidateSelection(d, s, local("2026-11-10 00:00"), true))
	d.StartStepMinutes = 60
	require.Error(t, ValidateSelection(d, s, local("2026-11-10 00:00"), true))
	d.BufferMinutes = 45
	require.Equal(t, s.EndsAt.Add(45*time.Minute), spanEnd(d, s))
}
func local(s string) time.Time {
	t, e := time.ParseInLocation("2006-01-02 15:04", s, Location)
	if e != nil {
		panic(e)
	}
	return t
}
func visit() Selection {
	return Selection{DepartmentID: "sauna", ResourceIDs: []string{"whole"}, StartsAt: local("2026-11-11 15:30"), EndsAt: local("2026-11-11 16:30"), Guests: []Guest{{30, 5}}}
}
func TestSaunaPriceAcrossTariffBoundary(t *testing.T) {
	d := sauna()
	s := visit()
	q, e := Calculate(d, s, nil)
	require.NoError(t, e)
	require.Equal(t, int64(170000), q.Amount)
	s.Guests = []Guest{{30, 6}}
	q, e = Calculate(d, s, nil)
	require.NoError(t, e)
	require.Equal(t, int64(204000), q.Amount)
	s.Guests = []Guest{{30, 5}, {12, 1}}
	q, e = Calculate(d, s, nil)
	require.NoError(t, e)
	require.Equal(t, int64(170000), q.Amount)
	s.Guests = []Guest{{30, 5}, {13, 1}}
	q, e = Calculate(d, s, nil)
	require.NoError(t, e)
	require.Equal(t, int64(204000), q.Amount)
	d.BaseGuests = 6
	q, e = Calculate(d, s, nil)
	require.NoError(t, e)
	require.Equal(t, int64(170000), q.Amount)
}
func TestRoundOnlyOnce(t *testing.T) {
	d := sauna()
	d.Tariffs.Rules[0].Hourly = 100001
	d.Tariffs.Rules[1].Hourly = 100001
	q, e := Calculate(d, visit(), nil)
	require.NoError(t, e)
	require.Equal(t, int64(100001), q.Amount)
}
func TestSaunaRules(t *testing.T) {
	d := sauna()
	s := visit()
	now := local("2026-11-10 00:00")
	require.NoError(t, ValidateSelection(d, s, now, true))
	s.EndsAt = s.StartsAt.Add(30 * time.Minute)
	require.Error(t, ValidateSelection(d, s, now, true))
	s = visit()
	s.StartsAt = s.StartsAt.Add(15 * time.Minute)
	s.EndsAt = s.EndsAt.Add(15 * time.Minute)
	require.Error(t, ValidateSelection(d, s, now, true))
	s = visit()
	s.Guests = []Guest{{30, 5}, {1, 6}}
	require.Error(t, ValidateSelection(d, s, now, true))
	s.Guests = []Guest{{12, 1}}
	require.Error(t, ValidateSelection(d, s, now, true))
	s = visit()
	s.ResourceIDs = []string{"whole", "whole"}
	require.Error(t, ValidateSelection(d, s, now, true))
}
func TestMonthOpeningIsCalendarRule(t *testing.T) {
	d := sauna()
	d.Schedule.Week["2"] = []Slot{{"09:00", "23:00", "mixed"}}
	s := visit()
	s.StartsAt = local("2026-12-01 15:30")
	s.EndsAt = s.StartsAt.Add(time.Hour)
	require.Error(t, ValidateSelection(d, s, local("2026-11-23 23:59"), true))
	require.NoError(t, ValidateSelection(d, s, local("2026-11-24 00:00"), true))
}
func TestScheduleExceptionsAndConflicts(t *testing.T) {
	d := sauna()
	require.NoError(t, ValidateSchedule(d.Schedule))
	d.Schedule.Exceptions = map[string][]Slot{"2026-11-11": {}}
	_, e := allowedSlot(d, visit())
	require.Error(t, e)
	require.NoError(t, ValidateSchedule(d.Schedule))
	d.Schedule.Week["3"] = []Slot{{"09:00", "12:00", "male"}, {"11:00", "14:00", "female"}}
	require.Error(t, ValidateSchedule(d.Schedule))
}
func TestTariffOverlapAndGaps(t *testing.T) {
	d := sauna()
	require.NoError(t, ValidateTariffs(d.Tariffs, "sauna"))
	d.Tariffs.Rules[1].StartMinute = 959
	require.Error(t, ValidateTariffs(d.Tariffs, "sauna"))
	d = sauna()
	d.Tariffs.Rules[1].StartMinute = 961
	_, e := Calculate(d, visit(), nil)
	require.Error(t, e)
}
func TestChildrenDoNotTakeSeats(t *testing.T) {
	d := sauna()
	d.Mode = "session"
	s := visit()
	s.StartsAt = local("2026-11-11 09:00")
	s.EndsAt = local("2026-11-11 11:00")
	d.Schedule.Week["3"] = []Slot{{"09:00", "11:00", "male"}}
	s.Guests = []Guest{{30, 1}, {7, 2}, {0, 1}}
	require.NoError(t, ValidateSelection(d, s, local("2026-11-10 00:00"), true))
	s.Guests = append(s.Guests, Guest{8, 1})
	require.Error(t, ValidateSelection(d, s, local("2026-11-10 00:00"), true))
}
func TestPhoneAndCapabilities(t *testing.T) {
	b, e := normalizeBuyer(Buyer{"  Иван  ", "8 (900) 123-45-67"})
	require.NoError(t, e)
	require.Equal(t, "+79001234567", b.Phone)
	require.Equal(t, "Иван", b.Name)
	_, e = normalizeBuyer(Buyer{"Иван", "123x4567890"})
	require.Error(t, e)
	require.NotEqual(t, capability("id", "key", "order"), capability("id", "key", "ticket"))
}
