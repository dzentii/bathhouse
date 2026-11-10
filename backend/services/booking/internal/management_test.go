package booking

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCatalogSetupValidation(t *testing.T) {
	d := sauna()
	v := platform.CatalogDepartment{ID: "new", Mode: "sauna", Active: true, Version: 1, Setup: &platform.CatalogSetup{
		BaseGuests: 5, MaxGuests: 10, MinDurationMinutes: 60, DurationStepMinutes: 60, StartStepMinutes: 30, BufferMinutes: 30,
		Schedule: bytesJSON(d.Schedule), Tariffs: bytesJSON(d.Tariffs), Resources: []platform.CatalogResource{{ID: uuid.NewString(), Label: "Сауна", X: 10, Y: 20}},
	}}
	_, err := setupDepartment(v)
	require.NoError(t, err)
	v.Setup.Resources = append(v.Setup.Resources, platform.CatalogResource{ID: uuid.NewString(), Label: "Другая"})
	_, err = setupDepartment(v)
	require.Error(t, err)
	v.Mode = "session"
	v.Setup.BufferMinutes = 0
	v.Setup.Tariffs = bytesJSON(Tariffs{Rules: []Tariff{{ID: "all", ValidFrom: "2020-01-01", ValidUntil: "2100-01-01", DayTypes: []string{"weekday", "weekend", "holiday"}, EndMinute: 1440, Adult: 10000, Child: 5000, DurationMinutes: 60}}})
	v.Setup.Resources[0].Label = "1"
	v.Setup.Resources[1].Label = " 1 "
	_, err = setupDepartment(v)
	require.Error(t, err, "normalized resource labels must be unique")
	v.Mode = "info"
	_, err = setupDepartment(v)
	require.Error(t, err)
	v.Setup = nil
	_, err = setupDepartment(v)
	require.NoError(t, err)
}

func TestBookingPolicyValidation(t *testing.T) {
	d := sauna()
	require.NoError(t, validatePolicy(d))
	d.BaseGuests = 11
	require.Error(t, validatePolicy(d))
	d = sauna()
	d.MinDurationMinutes = 90
	require.Error(t, validatePolicy(d))
	d = sauna()
	d.Mode = "session"
	require.Error(t, validatePolicy(d))
	d.BufferMinutes = 0
	require.NoError(t, validatePolicy(d))
	d.Archived = true
	require.Error(t, ValidateSelection(d, visit(), local("2026-11-10 00:00"), true))
}

func TestCalendarWindowAndDuration(t *testing.T) {
	d := sauna()
	require.False(t, saleDay(d, local("2026-12-01 00:00"), local("2026-11-23 23:59")))
	require.True(t, saleDay(d, local("2026-12-01 00:00"), local("2026-11-24 00:00")))
	d.Mode = "session"
	require.True(t, saleDay(d, local("2026-11-17 00:00"), local("2026-11-10 12:00")))
	require.False(t, saleDay(d, local("2026-11-18 00:00"), local("2026-11-10 12:00")))
	_, err := durationQuery(httptest.NewRequest("GET", "/?duration_minutes=61", nil), d)
	require.Error(t, err)
	value, err := durationQuery(httptest.NewRequest("GET", "/?duration_minutes=120", nil), d)
	require.NoError(t, err)
	require.Equal(t, 120, value)
}

func TestTicketPDFConcurrentLongCyrillic(t *testing.T) {
	d := sauna()
	q, err := Calculate(d, visit(), []Resource{{ID: uuid.NewString(), Label: strings.Repeat("ДлиннаяМетка", 30)}})
	require.NoError(t, err)
	q.BathName = "Сандуновские бани"
	q.BathAddress = "Екатеринбург, улица Тестовая, 1"
	q.DepartmentName = strings.Repeat("Сауна с кириллицей ", 20)
	ticket := Ticket{OrderID: uuid.NewString(), Visit: q, PaidAt: visit().StartsAt, Warning: ticketWarning, TestMode: true}
	var wg sync.WaitGroup
	errors := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := ticketPDF(ticket)
			if err == nil && (!bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.Contains(data, []byte("%%EOF"))) {
				errors <- platform.Bad("Invalid PDF")
			} else {
				errors <- err
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, "123,05", money(12305))
}
