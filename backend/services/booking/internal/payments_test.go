package booking

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPaymentSummaryUnavailableIsExplicit(t *testing.T) {
	for _, status := range []int{503, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/internal/order-payment-summaries" {
					t.Error("unexpected internal path")
				}
				if r.Header.Get("X-Internal-Key") != "unit-internal-key" {
					t.Error("missing internal authorization")
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("invalid upstream response"))
			}))
			defer server.Close()
			s := Service{Config: platform.Config{PaymentURL: server.URL, InternalKey: "unit-internal-key"}}
			err := s.paymentSummaries(httptest.NewRequest("GET", "/api/v1/admin/orders", nil), []Order{{ID: uuid.NewString()}})
			var api *platform.APIError
			require.True(t, errors.As(err, &api))
			require.Equal(t, 503, api.Status)
			require.NotContains(t, err.Error(), "upstream response")
		})
	}
}
