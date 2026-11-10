package platform

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestIdempotencyHeaderContract(t *testing.T) {
	key := uuid.NewString()
	for _, tc := range []struct {
		name, x, old string
		want         int
	}{
		{"missing", "", "", 400}, {"invalid", "123", "", 400},
		{"new", key, "", 200}, {"legacy", "", key, 200}, {"same", key, key, 200},
		{"conflicting", key, uuid.NewString(), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/orders", strings.NewReader(`{}`))
			r.Header.Set("X-Idempotency-Key", tc.x)
			r.Header.Set("Idempotency-Key", tc.old)
			w := httptest.NewRecorder()
			RequireIdempotency("test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, ok := r.Context().Value(requestKey{}).(requestIdentity)
				require.True(t, ok)
				w.WriteHeader(200)
			})).ServeHTTP(w, r)
			require.Equal(t, tc.want, w.Code)
		})
	}
}

func TestReceiptEncryption(t *testing.T) {
	plain := []byte(`{"name":"Вымышленный покупатель","phone":"+79000000000"}`)
	a, err := seal("test-key", plain)
	require.NoError(t, err)
	b, err := seal("test-key", plain)
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	require.False(t, bytes.Contains(a, []byte("+79000000000")))
	decoded, err := unseal("test-key", a)
	require.NoError(t, err)
	require.Equal(t, plain, decoded)
	_, err = unseal("wrong-key", a)
	require.Error(t, err)
	a[len(a)-1] ^= 1
	_, err = unseal("test-key", a)
	require.Error(t, err)
	_, err = unseal("test-key", []byte("short"))
	require.Error(t, err)
}
