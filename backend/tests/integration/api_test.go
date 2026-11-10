package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
)

type resource struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state"`
}
type selection struct {
	DepartmentID string           `json:"department_id"`
	ResourceIDs  []string         `json:"resource_ids"`
	StartsAt     time.Time        `json:"starts_at"`
	EndsAt       time.Time        `json:"ends_at"`
	Guests       []map[string]any `json:"guests"`
}
type quote struct {
	Amount    int64            `json:"amount_kopecks"`
	Hash      string           `json:"price_hash"`
	Resources []resource       `json:"resources"`
	Guests    []map[string]any `json:"guests"`
}
type order struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Selection selection  `json:"selection"`
	Quote     quote      `json:"quote"`
	Amount    int64      `json:"amount_kopecks"`
	ExpiresAt *time.Time `json:"expires_at"`
	TicketURL string     `json:"ticket_url"`
}
type created struct {
	Order order  `json:"order"`
	Token string `json:"order_token"`
}
type held struct {
	Order   order `json:"order"`
	Changed bool  `json:"price_changed"`
}
type attempt struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type suite struct {
	t       *testing.T
	base    string
	env     map[string]string
	db, pay *pgxpool.Pool
	redis   *redis.Client
	admin   string
	now     time.Time
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	if os.Getenv("BANYA_INTEGRATION") != "1" {
		t.Skip("set BANYA_INTEGRATION=1 after starting Compose with compose.local.yaml")
	}
	b, e := os.ReadFile(filepath.Join("..", "..", ".env"))
	require.NoError(t, e)
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			env[k] = v
		}
	}
	require.Equal(t, "development", env["APP_ENV"], "integration tests are restricted to the local test environment")
	s := &suite{t: t, base: "http://localhost:" + env["HTTP_PORT"], env: env}
	if v := os.Getenv("BANYA_TEST_URL"); v != "" {
		s.base = v
	}
	ctx := context.Background()
	s.db, e = pgxpool.New(ctx, "postgres://booking:"+env["BOOKING_DB_PASSWORD"]+"@localhost:55432/booking?sslmode=disable")
	require.NoError(t, e)
	require.NoError(t, s.db.Ping(ctx))
	t.Cleanup(s.db.Close)
	s.pay, e = pgxpool.New(ctx, "postgres://payment:"+env["PAYMENT_DB_PASSWORD"]+"@localhost:55432/payment?sslmode=disable")
	require.NoError(t, e)
	t.Cleanup(s.pay.Close)
	s.redis = redis.NewClient(&redis.Options{Addr: "localhost:56379", Password: env["REDIS_PASSWORD"]})
	t.Cleanup(func() { _ = s.redis.Close() })
	require.NoError(t, s.db.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&s.now))
	return s
}
func (s *suite) raw(method, path, token, key string, body any) (int, []byte, error) {
	code, b, _, err := s.rawResponse(method, path, token, key, body)
	return code, b, err
}
func (s *suite) rawResponse(method, path, token, key string, body any) (int, []byte, http.Header, error) {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, e := http.NewRequest(method, s.base+path, bytes.NewReader(data))
	if e != nil {
		return 0, nil, nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key == "" && (method == "POST" || method == "PUT" || method == "PATCH") {
		key = uuid.NewString()
	}
	if key != "" {
		req.Header.Set("X-Idempotency-Key", key)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	res, e := client.Do(req)
	if e != nil {
		return 0, nil, nil, e
	}
	defer func() { _ = res.Body.Close() }()
	b, e := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	return res.StatusCode, b, res.Header, e
}
func (s *suite) call(method, path, token, key string, body any, want int, out any) {
	s.t.Helper()
	if key == "" && (method == "POST" || method == "PUT" || method == "PATCH") {
		key = uuid.NewString()
	}
	code, b, headers, e := s.rawResponse(method, path, token, key, body)
	if e == nil && code == 429 && want != 429 {
		seconds, err := strconv.Atoi(headers.Get("Retry-After"))
		if err == nil && seconds >= 1 && seconds <= 60 {
			s.t.Logf("Shared HTTP budget exhausted; retrying the same request after %d seconds", seconds)
			time.Sleep(time.Duration(seconds) * time.Second)
			code, b, _, e = s.rawResponse(method, path, token, key, body)
		}
	}
	require.NoError(s.t, e)
	label := path
	if strings.Contains(path, "/tickets/") {
		label = "/api/v1/tickets/{token}"
	}
	if code != want {
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &failure)
		s.t.Fatalf("%s %s: wanted %d got %d (%s: %s)", method, label, want, code, failure.Error.Code, failure.Error.Message)
	}
	if out != nil {
		require.NoError(s.t, json.Unmarshal(b, out))
	}
}
func (s *suite) find(dep string, n int) selection {
	s.t.Helper()
	loc, _ := time.LoadLocation("Asia/Yekaterinburg")
	base := s.now.In(loc)
	for day := 0; day < 8; day++ {
		date := base.AddDate(0, 0, day).Format("2006-01-02")
		var a struct {
			Items []struct {
				StartsAt  time.Time  `json:"starts_at"`
				EndsAt    time.Time  `json:"ends_at"`
				Resources []resource `json:"resources"`
			} `json:"items"`
		}
		s.call("GET", "/api/v1/departments/"+dep+"/availability?date="+date, "", "", nil, 200, &a)
		for _, slot := range a.Items {
			ids := []string{}
			for _, r := range slot.Resources {
				if r.State == "free" {
					ids = append(ids, r.ID)
				}
			}
			if len(ids) >= n {
				return selection{dep, ids[:n], slot.StartsAt, slot.EndsAt, []map[string]any{{"age": 30, "count": n}}}
			}
		}
	}
	s.t.Fatalf("no available test slot for %s", dep)
	return selection{}
}
func (s *suite) create(sel selection) created {
	s.t.Helper()
	var c created
	s.call("POST", "/api/v1/orders", "", uuid.NewString(), map[string]any{"selection": sel, "buyer": map[string]string{"name": "Интеграционный тест", "phone": "+79000000000"}}, 201, &c)
	return c
}
func (s *suite) hold(c created) held {
	s.t.Helper()
	var h held
	s.call("POST", "/api/v1/orders/"+c.Order.ID+"/hold", c.Token, "", map[string]any{}, 200, &h)
	return h
}
func (s *suite) release(c created) {
	s.t.Helper()
	s.call("POST", "/api/v1/orders/"+c.Order.ID+"/release", c.Token, "", map[string]any{}, 200, nil)
}
func (s *suite) read(c created) order {
	s.t.Helper()
	var o order
	s.call("GET", "/api/v1/orders/"+c.Order.ID, c.Token, "", nil, 200, &o)
	return o
}
func (s *suite) waitAttempt(c created, a attempt, status string) {
	s.t.Helper()
	require.Eventually(s.t, func() bool {
		code, b, e := s.raw("GET", "/api/v1/orders/"+c.Order.ID+"/payments/"+a.ID, c.Token, "", nil)
		if e != nil || code != 200 {
			return false
		}
		var v attempt
		return json.Unmarshal(b, &v) == nil && v.Status == status
	}, 30*time.Second, 200*time.Millisecond, "payment status should become %s", status)
}
func (s *suite) login() {
	var result struct {
		Token string `json:"access_token"`
	}
	s.call("POST", "/api/v1/admin/sessions", "", "", map[string]string{"username": s.env["ADMIN_USERNAME"], "password": s.env["ADMIN_PASSWORD"]}, 201, &result)
	s.admin = result.Token
}
func paymentBody(h held, outcome string) map[string]any {
	return map[string]any{"amount_kopecks": h.Order.Amount, "currency": "RUB", "price_hash": h.Order.Quote.Hash, "outcome": outcome}
}

func TestEndToEnd(t *testing.T) {
	s := newSuite(t)
	t.Run("catalog_layouts_and_access", func(t *testing.T) {
		s.t = t
		var baths struct {
			Items []any `json:"items"`
		}
		s.call("GET", "/api/v1/baths", "", "", nil, 200, &baths)
		require.Len(t, baths.Items, 2)
		for _, v := range []struct {
			id    string
			count int
		}{{"1", 10}, {"2", 6}} {
			var d struct {
				Items  []any            `json:"items"`
				Groups map[string][]any `json:"groups"`
			}
			s.call("GET", "/api/v1/baths/"+v.id+"/departments", "", "", nil, 200, &d)
			require.Len(t, d.Items, v.count)
			require.Len(t, d.Groups, 3)
		}
		var layout struct {
			Resources []resource `json:"resources"`
		}
		s.call("GET", "/api/v1/departments/b1-female-kupel/resources", "", "", nil, 200, &layout)
		require.Len(t, layout.Resources, 18)
		labels := []string{}
		for _, r := range layout.Resources {
			labels = append(labels, r.Label)
		}
		require.Contains(t, labels, "111")
		require.NotContains(t, labels, "101")
		s.call("GET", "/api/v1/departments/b1-male-floor-1/resources", "", "", nil, 404, nil)
		var info struct {
			Description string `json:"description"`
		}
		s.call("GET", "/api/v1/departments/b2-shower-large", "", "", nil, 200, &info)
		require.Contains(t, info.Description, "12:30")
		require.Contains(t, info.Description, "350")
		s.call("GET", "/api/v1/admin/orders", "", "", nil, 401, nil)
		s.call("GET", "/internal/orders/x/payment-context", "", "", nil, 404, nil)
		ttl, e := s.redis.TTL(context.Background(), "catalog:v4:baths").Result()
		require.NoError(t, e)
		require.Greater(t, ttl, time.Duration(0))
		require.LessOrEqual(t, ttl, time.Minute)
	})
	t.Run("create_recovery_and_atomic_group_race", func(t *testing.T) {
		s.t = t
		sel := s.find("b1-male-general", 2)
		body := map[string]any{"selection": sel, "buyer": map[string]string{"name": "Тест повторов", "phone": "89000000000"}}
		key := uuid.NewString()
		var a, b created
		s.call("POST", "/api/v1/orders", "", key, body, 201, &a)
		s.call("POST", "/api/v1/orders", "", key, body, 201, &b)
		require.Equal(t, a.Order.ID, b.Order.ID)
		require.Equal(t, a.Token, b.Token)
		b = s.create(sel)
		start := make(chan struct{})
		results := make([]int, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, c := range []created{a, b} {
			wg.Add(1)
			go func(i int, c created) {
				defer wg.Done()
				<-start
				results[i], _, errs[i] = s.raw("POST", "/api/v1/orders/"+c.Order.ID+"/hold", c.Token, "", map[string]any{})
			}(i, c)
		}
		close(start)
		wg.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		require.ElementsMatch(t, []int{200, 409}, results)
		winner, loser := a, b
		if results[1] == 200 {
			winner, loser = b, a
		}
		o := s.read(winner)
		require.Equal(t, "held", o.Status)
		var count int
		require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM allocations WHERE active AND order_id=ANY($1::uuid[])", []string{a.Order.ID, b.Order.ID}).Scan(&count))
		require.Equal(t, 2, count)
		again := s.hold(winner)
		require.True(t, o.ExpiresAt.Equal(*again.Order.ExpiresAt))
		s.call("GET", "/api/v1/orders/"+winner.Order.ID, loser.Token, "", nil, 404, nil)
		s.release(winner)
		s.release(winner)
		s.hold(loser)
		s.release(loser)
	})
	t.Run("one_conflicting_seat_rolls_back_group", func(t *testing.T) {
		s.t = t
		sel := s.find("b1-male-general", 2)
		first := sel
		first.ResourceIDs = sel.ResourceIDs[:1]
		first.Guests = []map[string]any{{"age": 30, "count": 1}}
		a := s.create(first)
		s.hold(a)
		b := s.create(sel)
		s.call("POST", "/api/v1/orders/"+b.Order.ID+"/hold", b.Token, "", map[string]any{}, 409, nil)
		var count int
		require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM allocations WHERE order_id=$1", b.Order.ID).Scan(&count))
		require.Zero(t, count)
		s.release(a)
		s.release(b)
	})
	t.Run("expiry_and_late_success", func(t *testing.T) {
		s.t = t
		c := s.create(s.find("b1-female-general", 1))
		h := s.hold(c)
		require.Equal(t, "held", h.Order.Status)
		_, e := s.db.Exec(context.Background(), "UPDATE orders SET held_at=clock_timestamp()-interval '11 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", c.Order.ID)
		require.NoError(t, e)
		o := s.read(c)
		require.Equal(t, "expired", o.Status)
		pid := uuid.NewString()
		_, e = s.pay.Exec(context.Background(), "INSERT INTO attempts(id,order_id,idempotency_hash,request_hash,amount_kopecks,currency,outcome,status) VALUES($1,$2,$3,$4,$5,'RUB','success','pending')", pid, c.Order.ID, uuid.NewString(), "late-fixture", o.Amount)
		require.NoError(t, e)
		event := platform.Event{ID: uuid.NewString(), Type: "payment.result", Version: 1, AggregateID: c.Order.ID, OccurredAt: time.Now(), Data: mustJSON(platform.PaymentResult{PaymentID: pid, OrderID: c.Order.ID, Amount: o.Amount, Currency: "RUB", Outcome: "success"})}
		s.publish(event)
		s.waitAttempt(c, attempt{ID: pid}, "rejected")
		require.Equal(t, "expired", s.read(c).Status)
		var count int
		require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM allocations WHERE active AND order_id=$1", c.Order.ID).Scan(&count))
		require.Zero(t, count)
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/hold", c.Token, "", map[string]any{}, 409, nil)
	})
	t.Run("payment_decline_retry_ticket_and_duplicate_delivery", func(t *testing.T) {
		s.t = t
		s.login()
		sel := s.find("b1-female-general", 1)
		sel.Guests = append(sel.Guests, map[string]any{"age": 5, "count": 1}, map[string]any{"age": 1, "count": 1})
		c := s.create(sel)
		h := s.hold(c)
		require.Len(t, h.Order.Quote.Resources, 1)
		require.Len(t, h.Order.Quote.Guests, 3)
		bad := paymentBody(h, "success")
		bad["amount_kopecks"] = h.Order.Amount - 1
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, uuid.NewString(), bad, 409, nil)
		key := uuid.NewString()
		var a attempt
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, key, paymentBody(h, "declined"), 202, &a)
		s.waitAttempt(c, a, "declined")
		var declinedView map[string]any
		s.call("GET", "/api/v1/admin/orders/"+c.Order.ID, s.admin, "", nil, 200, &declinedView)
		require.Equal(t, "declined", declinedView["payment"].(map[string]any)["status"])
		require.Equal(t, float64(0), declinedView["payment"].(map[string]any)["paid_kopecks"])

		o := s.read(c)
		require.Equal(t, "held", o.Status)
		require.True(t, o.ExpiresAt.Equal(*h.Order.ExpiresAt))
		var repeat attempt
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, key, paymentBody(h, "declined"), 200, &repeat)
		require.Equal(t, a.ID, repeat.ID)
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, key, paymentBody(h, "success"), 409, nil)
		var success attempt
		key = uuid.NewString()
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, key, paymentBody(h, "success"), 202, &success)
		s.waitAttempt(c, success, "accepted")
		o = s.read(c)
		require.Equal(t, "confirmed", o.Status)
		var paidView map[string]any
		s.call("GET", "/api/v1/admin/orders/"+c.Order.ID, s.admin, "", nil, 200, &paidView)
		require.Equal(t, "accepted", paidView["payment"].(map[string]any)["status"])
		require.Equal(t, float64(o.Amount), paidView["payment"].(map[string]any)["paid_kopecks"])

		require.NotEmpty(t, o.TicketURL)
		var ticket map[string]any
		s.call("GET", strings.TrimPrefix(o.TicketURL, s.base), "", "", nil, 200, &ticket)
		require.Equal(t, true, ticket["test_mode"])
		require.NotContains(t, ticket, "buyer")
		s.call("GET", "/api/v1/tickets/"+strings.Repeat("0", 64), "", "", nil, 404, nil)
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/release", c.Token, "", map[string]any{}, 409, nil)
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, key, paymentBody(h, "success"), 200, &repeat)
		require.Equal(t, success.ID, repeat.ID)
		var payload []byte
		require.NoError(t, s.pay.QueryRow(context.Background(), "SELECT payload FROM outbox WHERE payload->>'type'='payment.result' AND payload->'data'->>'payment_id'=$1", success.ID).Scan(&payload))
		var event platform.Event
		require.NoError(t, json.Unmarshal(payload, &event))
		s.publish(event)
		s.publish(event)
		require.Eventually(t, func() bool {
			var count int
			e := s.db.QueryRow(context.Background(), "SELECT count(*) FROM payment_decisions WHERE payment_id=$1", success.ID).Scan(&count)
			return e == nil && count == 1
		}, 10*time.Second, 100*time.Millisecond)
		require.Equal(t, o.TicketURL, s.read(c).TicketURL)
	})
	t.Run("sauna_overlap_buffer_and_guests", func(t *testing.T) {
		s.t = t
		sel := s.find("b1-sauna-pool-1", 1)
		sel.Guests = []map[string]any{{"age": 30, "count": 5}, {"age": 12, "count": 1}}
		a := s.create(sel)
		s.hold(a)
		b := s.create(sel)
		s.call("POST", "/api/v1/orders/"+b.Order.ID+"/hold", b.Token, "", map[string]any{}, 409, nil)
		next := sel
		next.StartsAt = sel.EndsAt
		next.EndsAt = next.StartsAt.Add(time.Hour)
		code, body, e := s.raw("POST", "/api/v1/orders", "", uuid.NewString(), map[string]any{"selection": next, "buyer": map[string]string{"name": "Буфер", "phone": "+79000000000"}})
		require.NoError(t, e)
		if code == 201 {
			var c created
			require.NoError(t, json.Unmarshal(body, &c))
			s.call("POST", "/api/v1/orders/"+c.Order.ID+"/hold", c.Token, "", map[string]any{}, 409, nil)
			s.release(c)
		} else {
			require.Equal(t, 409, code)
		}
		s.release(a)
		s.release(b)
		tooMany := sel
		tooMany.Guests = []map[string]any{{"age": 30, "count": 11}}
		s.call("POST", "/api/v1/quotes", "", "", tooMany, 400, nil)
	})
	t.Run("admin_versions_fixed_price_conflicts_and_blocks", func(t *testing.T) {
		s.t = t
		s.login()
		sel := s.find("b1-male-general", 2)
		one := sel
		one.ResourceIDs = sel.ResourceIDs[:1]
		one.Guests = []map[string]any{{"age": 30, "count": 1}}
		fixed := s.create(one)
		h := s.hold(fixed)
		second := one
		second.ResourceIDs = sel.ResourceIDs[1:]
		draft := s.create(second)
		var config map[string]any
		s.call("GET", "/api/v1/departments/b1-male-general/booking-config", "", "", nil, 200, &config)
		oldVersion := int64(config["tariff_version"].(float64))
		tariffs := config["tariffs"].(map[string]any)
		for _, raw := range tariffs["rules"].([]any) {
			rule := raw.(map[string]any)
			rule["adult_kopecks"] = rule["adult_kopecks"].(float64) + 100
		}
		body := map[string]any{"version": oldVersion, "tariffs": tariffs}
		s.call("PUT", "/api/v1/admin/tariffs/b1-male-general", s.admin, "", body, 200, nil)
		s.call("PUT", "/api/v1/admin/tariffs/b1-male-general", s.admin, "", body, 409, nil)
		require.Equal(t, h.Order.Amount, s.read(fixed).Amount)
		updated := s.hold(draft)
		require.True(t, updated.Changed)
		require.Equal(t, draft.Order.Amount+100, updated.Order.Amount)
		var after map[string]any
		s.call("GET", "/api/v1/departments/b1-male-general/booking-config", "", "", nil, 200, &after)
		require.Equal(t, float64(oldVersion+1), after["tariff_version"])
		schedule := config["schedule"].(map[string]any)
		exceptions := schedule["exceptions"].(map[string]any)
		loc, _ := time.LoadLocation("Asia/Yekaterinburg")
		exceptions[sel.StartsAt.In(loc).Format("2006-01-02")] = []any{}
		s.call("PUT", "/api/v1/admin/schedules/b1-male-general", s.admin, "", map[string]any{"version": config["schedule_version"], "schedule": schedule}, 409, nil)
		blockBody := map[string]any{"department_id": sel.DepartmentID, "resource_ids": one.ResourceIDs, "starts_at": sel.StartsAt, "ends_at": sel.EndsAt, "reason": "Тест конфликта"}
		s.call("POST", "/api/v1/admin/resource-blocks", s.admin, "", blockBody, 409, nil)
		s.release(fixed)
		s.release(draft)
		var block struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		}
		s.call("POST", "/api/v1/admin/resource-blocks", s.admin, "", blockBody, 201, &block)
		blocked := s.create(one)
		s.call("POST", "/api/v1/orders/"+blocked.Order.ID+"/hold", blocked.Token, "", map[string]any{}, 409, nil)
		s.call("DELETE", fmt.Sprintf("/api/v1/admin/resource-blocks/%s?version=99", block.ID), s.admin, "", nil, 409, nil)
		s.call("DELETE", fmt.Sprintf("/api/v1/admin/resource-blocks/%s?version=%d", block.ID, block.Version), s.admin, "", nil, 200, nil)
		s.hold(blocked)
		s.release(blocked)
		s.call("GET", "/api/v1/admin/orders?status=confirmed&limit=10", s.admin, "", nil, 200, nil)
		s.call("GET", "/api/v1/admin/audit?limit=10", s.admin, "", nil, 200, nil)
		s.call("DELETE", "/api/v1/admin/sessions/current", s.admin, "", nil, 204, nil)
		s.call("GET", "/api/v1/admin/orders", s.admin, "", nil, 401, nil)
	})
}
func mustJSON(v any) json.RawMessage {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func (s *suite) publish(e platform.Event) {
	s.t.Helper()
	w := &kafka.Writer{Addr: kafka.TCP("localhost:19092"), Topic: "banya.events.v1", RequiredAcks: kafka.RequireAll}
	defer func() { _ = w.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(s.t, w.WriteMessages(ctx, kafka.Message{Key: []byte(e.AggregateID), Value: mustJSON(e)}))
}
