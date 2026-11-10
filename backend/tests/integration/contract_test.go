package integration

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestWriteContractAndAdminReplay(t *testing.T) {
	s := newSuite(t)
	s.login()
	// Explicitly bypass the positive helper that always supplies a fresh key.
	r, err := http.NewRequest("POST", s.base+"/api/v1/quotes", bytes.NewBufferString(`{}`))
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	require.Equal(t, 400, res.StatusCode)
	_ = res.Body.Close()
	const path = "/api/v1/admin/tariffs/b2-male-general"
	var config map[string]any
	s.call("GET", path, s.admin, "", nil, 200, &config)
	body := map[string]any{"version": config["tariff_version"], "tariffs": config["tariffs"]}
	key := uuid.NewString()
	// Two simultaneous retries change the version exactly once and return identical results.
	codes := make([]int, 2)
	responses := make([][]byte, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i], responses[i], errs[i] = s.raw("PUT", path, s.admin, key, body) }()
	}
	wg.Wait()
	for i := range 2 {
		require.NoError(t, errs[i])
		require.Equal(t, 200, codes[i])
	}
	require.JSONEq(t, string(responses[0]), string(responses[1]))
	// A still-running first request has a bounded, explicit response; retry stays safe.
	tx, err := s.db.Begin(context.Background())
	require.NoError(t, err)
	var scope string
	require.NoError(t, tx.QueryRow(context.Background(), "SELECT scope FROM api_requests WHERE key_hash=$1", platform.Hash(key)).Scan(&scope))
	_, err = tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", scope+":"+platform.Hash(key))
	require.NoError(t, err)
	code, _, requestErr := s.raw("PUT", path, s.admin, key, body)
	require.NoError(t, tx.Rollback(context.Background()))
	require.NoError(t, requestErr)
	require.Equal(t, 409, code)
	s.call("PUT", path, s.admin, key, body, 200, nil)

	body["version"] = config["tariff_version"].(float64) + 1
	s.call("PUT", path, s.admin, key, body, 409, nil)
	s.call("PUT", path, "", key, body, 401, nil)
	s.call("GET", "/api/v1/admin/resource-blocks?department_id=b2-male-general", s.admin, "", nil, 200, nil)

	infoPath := "/api/v1/admin/departments/b2-shower-large/info"
	var before map[string]any
	s.call("GET", infoPath, s.admin, "", nil, 200, &before)
	info := map[string]any{"version": before["version"], "description": before["description"]}
	key = uuid.NewString()
	var updated, again map[string]any
	s.call("PUT", infoPath, s.admin, key, info, 200, &updated)
	s.call("PUT", infoPath, s.admin, key, info, 200, &again)
	require.Equal(t, updated, again)
	var published map[string]any
	s.call("GET", "/api/v1/departments/b2-shower-large", "", "", nil, 200, &published)
	require.Equal(t, updated["version"], published["version"])
	require.Equal(t, updated["description"], published["description"])
	invalid := s.find("b2-male-general", 1)
	invalid.DepartmentID = "b2-shower-large"
	s.call("POST", "/api/v1/quotes", "", "", invalid, 404, nil)
	s.call("POST", "/api/v1/orders", "", "", map[string]any{"selection": invalid, "buyer": map[string]string{"name": "Тест запрета", "phone": "+79000000000"}}, 404, nil)

}

func TestOrderReplayAndRetention(t *testing.T) {
	s := newSuite(t)
	s.login()
	sel := s.find("b2-female-general", 1)
	sel.Guests = []map[string]any{{"category": "adult", "count": 1}, {"category": "child", "age": 5, "count": 1}}
	body := map[string]any{"selection": sel, "buyer": map[string]string{"name": "Тест срока хранения", "phone": "+79000000000"}}
	quoteKey := uuid.NewString()
	var q1, q2 quote
	s.call("POST", "/api/v1/quotes", "", quoteKey, sel, 200, &q1)
	s.call("POST", "/api/v1/quotes", "", quoteKey, sel, 200, &q2)
	require.Equal(t, q1, q2)
	key := uuid.NewString()
	var c, again created
	s.call("POST", "/api/v1/orders", "", key, body, 201, &c)
	s.call("POST", "/api/v1/orders", "", key, body, 201, &again)
	require.Equal(t, c.Order.ID, again.Order.ID)
	var h, h2 held
	holdKey := uuid.NewString()
	path := "/api/v1/orders/" + c.Order.ID + "/hold"
	s.call("POST", path, c.Token, holdKey, map[string]any{}, 200, &h)
	s.call("POST", path, c.Token, holdKey, map[string]any{}, 200, &h2)
	require.Equal(t, h.Order.ExpiresAt, h2.Order.ExpiresAt)
	s.call("POST", path, "wrong", holdKey, map[string]any{}, 404, nil)
	require.Equal(t, "adult", h.Order.Quote.Guests[0]["category"])
	require.NotContains(t, h.Order.Quote.Guests[0], "age")
	s.release(c)
	ctx := context.Background()
	short := s.create(sel)
	_, shortErr := s.db.Exec(ctx, "UPDATE orders SET access_expires_at=clock_timestamp()+interval '5 minutes' WHERE id=$1", short.Order.ID)
	require.NoError(t, shortErr)
	s.call("POST", "/api/v1/orders/"+short.Order.ID+"/hold", short.Token, "", map[string]any{}, 409, nil)
	s.release(short)

	_, err := s.db.Exec(ctx, "UPDATE orders SET access_expires_at=clock_timestamp()-interval '1 second',contact_purge_at=clock_timestamp()-interval '1 second' WHERE id=$1", c.Order.ID)
	require.NoError(t, err)
	s.call("GET", "/api/v1/orders/"+c.Order.ID, c.Token, "", nil, 404, nil)
	var adminView map[string]any
	s.call("GET", "/api/v1/admin/orders/"+c.Order.ID, s.admin, "", nil, 200, &adminView)
	require.Empty(t, adminView["buyer"])

	s.call("POST", "/api/v1/orders", "", key, body, 404, nil)
	s.call("POST", path, c.Token, holdKey, map[string]any{}, 404, nil)
	// Test physical worker cleanup as well as logical expiry; no production table is truncated.
	_, err = s.db.Exec(ctx, "UPDATE api_requests SET expires_at=clock_timestamp()-interval '1 second' WHERE scope='order:create' AND key_hash=$1", platformHash(key))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		var purged bool
		err = s.db.QueryRow(ctx, `SELECT contacts_purged_at IS NOT NULL AND buyer='{}'::jsonb AND access_hash='' AND request_hash=''
 AND NOT EXISTS(SELECT 1 FROM api_requests WHERE scope='order:create' AND key_hash=$2) FROM orders WHERE id=$1`, c.Order.ID, platformHash(key)).Scan(&purged)
		return err == nil && purged
	}, 75*time.Second, 500*time.Millisecond)
}

func TestLoginRetryCannotReviveRevokedSession(t *testing.T) {
	s := newSuite(t)
	key := uuid.NewString()
	body := map[string]string{"username": s.env["ADMIN_USERNAME"], "password": s.env["ADMIN_PASSWORD"]}
	var first, again struct {
		Token string `json:"access_token"`
		CSRF  string `json:"csrf_token"`
	}
	s.call("POST", "/api/v1/admin/sessions", "", key, body, 201, &first)
	s.call("POST", "/api/v1/admin/sessions", "", key, body, 201, &again)
	require.Equal(t, first, again)
	s.call("DELETE", "/api/v1/admin/sessions/current", first.Token, "", nil, 204, nil)
	s.call("POST", "/api/v1/admin/sessions", "", key, body, 401, nil)
}

func platformHash(key string) string {
	// Keep test keys out of SQL result/log output.
	return platform.Hash(key)
}
