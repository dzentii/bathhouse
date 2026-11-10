package platform

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"
)

type HistoryEntry struct {
	ID        string          `json:"id"`
	Source    string          `json:"source"`
	Action    string          `json:"action"`
	EntityID  string          `json:"entity_id"`
	ActorID   string          `json:"actor_id,omitempty"`
	Before    json.RawMessage `json:"before,omitempty"`
	After     json.RawMessage `json:"after,omitempty"`
	Details   json.RawMessage `json:"details,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type HistoryWindow struct {
	Limit    int
	Before   time.Time
	BeforeID string
}

func HistoryQuery(r *http.Request) (HistoryWindow, error) {
	w := HistoryWindow{Limit: 50, Before: time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), BeforeID: "~"}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return w, Bad("limit: 1..100")
		}
		w.Limit = n
	}
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return w, Bad("before должен иметь формат RFC3339")
		}
		w.Before = t
	}
	if v := r.URL.Query().Get("before_id"); v != "" {
		if !SafeText(v, 1, 128, false) {
			return w, Bad("Некорректный before_id")
		}
		w.BeforeID = v
	}
	if r.URL.Query().Get("offset") != "" {
		return w, Bad("Для истории используйте before и before_id вместо offset")
	}
	return w, nil
}
func HistoryResponse(w http.ResponseWriter, items []HistoryEntry, limit int) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	response := map[string]any{"items": items}
	if len(items) == limit {
		last := items[len(items)-1]
		response["next_before"] = last.CreatedAt
		response["next_before_id"] = last.ID
	}
	JSON(w, 200, response)
}
