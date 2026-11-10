package identity

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestPasswordByteLimitAndAgent(t *testing.T) {
	require.False(t, validPassword("short"))
	require.True(t, validPassword(strings.Repeat("я", 36)))
	require.False(t, validPassword(strings.Repeat("я", 37)))
	require.Equal(t, "BrowserAgent", safeAgent("Browser\r\nAgent\x7f"))
	require.Len(t, []rune(safeAgent(strings.Repeat("я", 300))), 200)
}

func TestOwnPasswordResetRejectsUUIDAliases(t *testing.T) {
	const id = "abcdefab-cdef-4abc-8def-abcdefabcdef"
	for _, alias := range []string{id, strings.ToUpper(id), "urn:uuid:" + id, strings.ReplaceAll(id, "-", "")} {
		t.Run(alias, func(t *testing.T) {
			r := httptest.NewRequest("PATCH", "/", strings.NewReader(`{"version":1,"password":"synthetic-new-password"}`))
			rc := chi.NewRouteContext()
			rc.URLParams.Add("employeeID", alias)
			r = r.WithContext(context.WithValue(platform.WithAdmin(r.Context(), platform.Admin{ID: id, Role: "owner"}), chi.RouteCtxKey, rc))
			w := httptest.NewRecorder()
			// Invalid self-reset must be rejected before opening any database transaction.
			require.NotPanics(t, func() { (&Service{}).patchEmployee(w, r) })
			require.Equal(t, 400, w.Code)
		})
	}
}
