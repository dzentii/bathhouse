package platform

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHistoryCursorAndStableMerge(t *testing.T) {
	for _, query := range []string{"limit=0", "limit=101", "before=bad", "offset=1", "before_id=%0A"} {
		_, err := HistoryQuery(httptest.NewRequest("GET", "/?"+query, nil))
		require.Error(t, err, query)
	}
	w, err := HistoryQuery(httptest.NewRequest("GET", "/?limit=2&before=2026-11-10T01:00:00Z&before_id=payment:abc", nil))
	require.NoError(t, err)
	require.Equal(t, 2, w.Limit)
	require.Equal(t, "payment:abc", w.BeforeID)
	now := time.Now().UTC()
	response := httptest.NewRecorder()
	HistoryResponse(response, []HistoryEntry{{ID: "a", CreatedAt: now}, {ID: "c", CreatedAt: now}, {ID: "b", CreatedAt: now}}, 2)
	var body struct {
		Items  []HistoryEntry `json:"items"`
		NextID string         `json:"next_before_id"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "c", body.Items[0].ID)
	require.Equal(t, "b", body.NextID)
}
