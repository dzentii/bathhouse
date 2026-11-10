package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func assertResourceConflict(t *testing.T, body []byte, ids []string) {
	t.Helper()
	expected := map[string]any{"error": map[string]any{
		"code":    "resource_unavailable",
		"message": "Часть выбора недоступна; ни один ресурс не удержан",
		"details": map[string]any{"resource_ids": ids},
	}}
	// Exact JSON equality also rejects accidental disclosure of order or buyer data.
	require.JSONEq(t, string(mustJSON(expected)), string(body))
}

func TestResourceConflictDetails(t *testing.T) {
	s := newSuite(t)
	s.login()
	sel := s.find("b1-male-general", 4)
	one := func(index int) selection {
		v := sel
		v.ResourceIDs = sel.ResourceIDs[index : index+1]
		v.Guests = []map[string]any{{"category": "adult", "count": 1}}
		return v
	}
	heldOrder := s.create(one(0))
	s.hold(heldOrder)
	t.Cleanup(func() { s.release(heldOrder) })
	paidOrder := s.create(one(1))
	h := s.hold(paidOrder)
	var payment attempt
	s.call("POST", "/api/v1/orders/"+paidOrder.Order.ID+"/payments", paidOrder.Token, "", paymentBody(h, "success"), 202, &payment)
	s.waitAttempt(paidOrder, payment, "accepted")
	var block struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	s.call("POST", "/api/v1/admin/resource-blocks", s.admin, "", map[string]any{
		"department_id": sel.DepartmentID, "resource_ids": one(2).ResourceIDs,
		"starts_at": sel.StartsAt, "ends_at": sel.EndsAt, "reason": "Проверка детализации конфликта",
	}, 201, &block)
	t.Cleanup(func() {
		s.call("DELETE", fmt.Sprintf("/api/v1/admin/resource-blocks/%s?version=%d", block.ID, block.Version), s.admin, "", nil, 200, nil)
	})
	candidate := s.create(sel)
	t.Cleanup(func() { s.release(candidate) })
	path := "/api/v1/orders/" + candidate.Order.ID + "/hold"
	key := uuid.NewString()
	var failure map[string]any
	s.call("POST", path, candidate.Token, key, map[string]any{}, 409, &failure)
	assertResourceConflict(t, mustJSON(failure), sel.ResourceIDs[:3])
	require.Equal(t, "draft", s.read(candidate).Status)
	var count int
	require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM allocations WHERE order_id=$1", candidate.Order.ID).Scan(&count))
	require.Zero(t, count)

	var hidden map[string]any
	s.call("POST", path, paidOrder.Token, "", map[string]any{}, 404, &hidden)
	require.NotContains(t, hidden["error"], "details")

	// Failed attempts are not cached: retry reflects released holds immediately.
	s.release(heldOrder)
	s.call("POST", path, candidate.Token, key, map[string]any{}, 409, &failure)
	assertResourceConflict(t, mustJSON(failure), sel.ResourceIDs[1:3])
}
