package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrustedClientAddress(t *testing.T) {
	c := Config{TrustedProxyCIDRs: []string{"10.0.0.0/24"}}
	for _, tc := range []struct{ peer, forwarded, want string }{
		{"198.51.100.7:1234", "1.2.3.4", "198.51.100.7"},
		{"10.0.0.1:1234", "1.2.3.4, 198.51.100.7", "198.51.100.7"},
		{"10.0.0.1:1234", "198.51.100.7, 10.0.0.2", "198.51.100.7"},
		{"10.0.0.1:1234", "invalid", "10.0.0.1"},
		{"[::ffff:198.51.100.7]:1234", "1.2.3.4", "198.51.100.7"},
	} {
		t.Run(tc.forwarded+tc.peer, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			require.Equal(t, tc.want, ResolveClientAddress(r, c))
		})
	}
}
func TestAdmissionHeavyBudget(t *testing.T) {
	gate := Admission(64)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan struct{}, 2)
	handler := gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release; w.WriteHeader(204) }))
	defer func() { close(release); <-done; <-done }()
	for range 2 {
		go func() {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/tickets/x/pdf", nil))
			done <- struct{}{}
		}()
		<-entered
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/tickets/x/pdf", nil))
	require.Equal(t, 503, w.Code)
	// The ordinary budget remains available while expensive operations are busy.
	light := gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	w = httptest.NewRecorder()
	light.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/baths", nil))
	require.Equal(t, 204, w.Code)
}
func TestStartupSecretsRedacted(t *testing.T) {
	const marker = "synthetic-secret-marker"
	_, err := Redis("redis://:" + marker + "%ZZ@localhost/0")
	require.Error(t, err)
	require.NotContains(t, err.Error(), marker)
	require.NotContains(t, SafeStartupError(err), marker)
	require.NotContains(t, SafeStartupError(errors.New(marker)), marker)
	require.Equal(t, "invalid REDIS_URL configuration", SafeStartupError(err))
}

func TestRedisBudgets(t *testing.T) {
	client, err := Redis("redis://127.0.0.1:1/0")
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	options := client.Options()
	require.Equal(t, 1, options.DialerRetries)
	require.Equal(t, 12, options.MaxActiveConns)
	require.True(t, options.ContextTimeoutEnabled)
	require.LessOrEqual(t, options.PoolTimeout, options.DialTimeout)
}
func TestClientKeysAndRateBudgets(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/baths", nil)
	r.RemoteAddr = "198.51.100.7:1234"
	key := ClientKey(r, strings.Repeat("x", 32))
	require.Len(t, key, 64)
	require.NotContains(t, key, "198.51.100")
	r = r.WithContext(context.WithValue(r.Context(), addressKey{}, "198.51.100.8"))
	require.NotEqual(t, key, ClientKey(r, strings.Repeat("x", 32)))
	group, max := rateBudget(r, Config{})
	require.Equal(t, "read", group)
	require.Equal(t, 1200, max)
	r.Method = "POST"
	group, max = rateBudget(r, Config{})
	require.Equal(t, "write", group)
	require.Equal(t, 300, max)
	r.URL.Path = "/api/v1/departments/x/availability-calendar"
	group, max = rateBudget(r, Config{})
	require.Equal(t, "heavy", group)
	require.Equal(t, 60, max)
}

func TestOperationalReadBudgets(t *testing.T) {
	for _, path := range []string{"/api/v1/admin/orders/search", "/api/v1/admin/departments/x/occupancy", "/api/v1/admin/security-events"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if strings.HasSuffix(path, "/search") {
			r.Method = http.MethodPost
		}
		group, maximum := rateBudget(r, Config{})
		require.Equal(t, "heavy", group, path)
		require.Equal(t, 60, maximum, path)
	}
}
