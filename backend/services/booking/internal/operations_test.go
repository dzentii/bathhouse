package booking

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSalesPauseAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	enabled, disabled := true, false
	reason := "Технические работы"
	until := now.Add(time.Minute)
	d := Department{OnlineSalesEnabled: true}
	paused, err := applySalesPatch(d, salesPatch{Enabled: &disabled, Reason: &reason, Until: json.RawMessage(`"` + until.Format(time.RFC3339) + `"`)}, now)
	require.NoError(t, err)
	require.Error(t, requireSales(paused, now))
	require.NoError(t, requireSales(paused, until))
	view := salesView(paused, until)
	require.True(t, view.OnlineSalesEnabled)
	require.Nil(t, view.SalesPausedUntil)
	require.Empty(t, view.SalesPauseReason)
	require.False(t, paused.OnlineSalesEnabled, "normalizing a view must not mutate the raw cached value")
	resumed, err := applySalesPatch(paused, salesPatch{Enabled: &enabled}, now)
	require.NoError(t, err)
	require.Nil(t, resumed.SalesPausedUntil)
	require.Empty(t, resumed.SalesPauseReason)
	indefinite, err := applySalesPatch(paused, salesPatch{Until: json.RawMessage(`null`)}, now)
	require.NoError(t, err)
	require.Error(t, requireSales(indefinite, until))
	d.Archived = true
	require.Error(t, requireSales(d, now))
}
func TestSalesPauseInput(t *testing.T) {
	now := time.Now().UTC()
	enabled, disabled := true, false
	reason := "Работы"
	blank := " "
	for _, p := range []salesPatch{{Enabled: &disabled}, {Enabled: &disabled, Reason: &blank}, {Enabled: &disabled, Reason: &reason, Until: json.RawMessage(`"invalid"`)}, {Enabled: &disabled, Reason: &reason, Until: json.RawMessage(`"` + now.Add(-time.Second).Format(time.RFC3339) + `"`)}, {Enabled: &enabled, Reason: &reason}} {
		_, err := applySalesPatch(Department{OnlineSalesEnabled: true}, p, now)
		require.Error(t, err)
	}
	var in struct {
		salesPatch
		Version int64 `json:"version"`
	}
	r := httptest.NewRequest("PATCH", "/", strings.NewReader(`{"version":1,"online_sales_enabled":false,"sales_paused_until":null,"sales_pause_reason":"Работы"}`))
	require.NoError(t, platform.Decode(httptest.NewRecorder(), r, &in))
	require.True(t, in.present())
	require.NotNil(t, in.Enabled)
	require.False(t, *in.Enabled)
	require.Equal(t, "null", string(in.Until))
}
func TestOrderSearchFilters(t *testing.T) {
	from := time.Now()
	until := from.Add(time.Hour)
	bath := 1
	where, args, err := searchFilter(orderSearch{Phone: "8 (999) 123-45-67", OrderID: uuid.NewString(), BathID: &bath, DepartmentID: "dep", ResourceID: "resource", Status: "held", From: &from, Until: &until})
	require.NoError(t, err)
	require.Len(t, args, 8)
	require.Contains(t, args, "+79991234567")
	require.NotContains(t, where, "79991234567")
	require.Contains(t, where, "contacts_purged_at IS NULL")
	require.Contains(t, where, "contact_purge_at>clock_timestamp()")
	for _, in := range []orderSearch{{OrderID: "invalid"}, {Phone: "injection' OR 1=1"}, {DepartmentID: "bad\x00"}, {From: &until, Until: &from}} {
		_, _, err = searchFilter(in)
		require.Error(t, err)
	}
}
func TestOccupancyRange(t *testing.T) {
	for _, query := range []string{"", "?from=2026-11-10&until=2026-11-11", "?from=2026-11-10T00:00:00Z&until=2026-11-10T00:00:00Z", "?from=2026-11-10T00:00:00Z&until=2027-01-10T00:00:00Z"} {
		_, _, err := occupancyWindow(httptest.NewRequest("GET", "/"+query, nil))
		require.Error(t, err)
	}
	from, until, err := occupancyWindow(httptest.NewRequest("GET", "/?from=2026-11-10T00:00:00Z&until=2026-11-11T00:00:00Z", nil))
	require.NoError(t, err)
	require.Equal(t, 24*time.Hour, until.Sub(from))
}
