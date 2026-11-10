package platform

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDecodeRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	for _, body := range []string{`{"name":"x","unexpected":1}`, `{"name":"x"} {"name":"y"}`, `{"name":""}`} {
		var in struct {
			Name string `json:"name" validate:"required"`
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		require.Error(t, Decode(w, r, &in))
	}
}

func TestRequestIDDoesNotLogClientMetadata(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	router := Router(Config{}, nil)
	router.Get("/api/v1/request-id-probe", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(middleware.GetReqID(r.Context())))
	})
	for _, marker := range []string{"private-client-token-marker", strings.Repeat("a", 4096)} {
		r := httptest.NewRequest("GET", "/api/v1/request-id-probe", nil)
		r.Header.Set("X-Request-ID", marker)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code)
		require.NotEqual(t, marker, w.Body.String())
		require.NoError(t, uuid.Validate(w.Body.String()))
		require.Equal(t, w.Body.String(), w.Header().Get("X-Request-ID"))
		require.NotContains(t, logs.String(), marker)
	}
}
func TestInternalAuthorization(t *testing.T) {
	called := false
	h := Internal("a-secret-internal-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/internal", nil))
	require.Equal(t, 401, w.Code)
	require.False(t, called)
	r := httptest.NewRequest("GET", "/internal", nil)
	r.Header.Set("X-Internal-Key", "a-secret-internal-key")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 204, w.Code)
	require.True(t, called)
}
func TestPublicErrorsDoNotExposeDatabaseText(t *testing.T) {
	w := httptest.NewRecorder()
	Fail(w, errors.New("database connection failed: postgres://user:private-password@host/db"))
	require.Equal(t, 500, w.Code)
	require.Contains(t, w.Body.String(), "internal_error")
	require.NotContains(t, w.Body.String(), "private-password")
	require.NotContains(t, w.Body.String(), "postgres://")
}

func TestResourceConflictDetails(t *testing.T) {
	w := httptest.NewRecorder()
	err := &APIError{
		Status:  http.StatusConflict,
		Code:    "resource_unavailable",
		Message: "Часть выбора недоступна; ни один ресурс не удержан",
		Details: &ErrorDetails{ResourceIDs: []string{"seat-2", "seat-5"}},
	}
	Fail(w, err)
	require.Equal(t, http.StatusConflict, w.Code)
	require.JSONEq(t, `{"error":{"code":"resource_unavailable","message":"Часть выбора недоступна; ни один ресурс не удержан","details":{"resource_ids":["seat-2","seat-5"]}}}`, w.Body.String())

	w = httptest.NewRecorder()
	Fail(w, Conflict("version_conflict", "Версия изменена"))
	require.JSONEq(t, `{"error":{"code":"version_conflict","message":"Версия изменена"}}`, w.Body.String())
}
