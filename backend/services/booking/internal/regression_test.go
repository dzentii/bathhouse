package booking

import (
	"bytes"
	"context"

	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type auditTracer struct{ n atomic.Int64 }

func (a *auditTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	a.n.Add(1)
	return ctx
}
func (a *auditTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func auditService(t *testing.T) (*Service, *auditTracer) {
	t.Helper()
	dsn := os.Getenv("BANYA_REGRESSION_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated audit database required")
	}
	c, e := pgxpool.ParseConfig(dsn)
	require.NoError(t, e)
	tr := &auditTracer{}
	c.ConnConfig.Tracer = tr
	c.MaxConns = 4
	db, e := pgxpool.NewWithConfig(context.Background(), c)
	require.NoError(t, e)
	t.Cleanup(db.Close)
	rc := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond, ReadTimeout: 10 * time.Millisecond, WriteTimeout: 10 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rc.Close() })
	return &Service{DB: db, Redis: rc, Config: platform.Config{InternalKey: strings.Repeat("a", 64), OrderKey: strings.Repeat("b", 64), TicketKey: strings.Repeat("c", 64), PublicURL: "http://localhost"}}, tr
}
func auditSelection(t *testing.T, s *Service, id string) Selection {
	t.Helper()
	ctx := context.Background()
	d, e := department(ctx, s.DB, id, false)
	require.NoError(t, e)
	rs, e := resources(ctx, s.DB, id)
	require.NoError(t, e)
	now, e := nowDB(ctx, s.DB)
	require.NoError(t, e)
	for day := 1; day < 7; day++ {
		date := dayStart(now).AddDate(0, 0, day)
		for _, sl := range slotsFor(d.Schedule, date) {
			a, _ := minute(sl.Start)
			b, _ := minute(sl.End)
			sel := Selection{DepartmentID: id, ResourceIDs: []string{rs[0].ID}, StartsAt: date.Add(time.Duration(a) * time.Minute), EndsAt: date.Add(time.Duration(b) * time.Minute), Guests: []Guest{{Age: 18, Count: 1}}}
			if d.Mode == "sauna" {
				sel.EndsAt = sel.StartsAt.Add(time.Hour)
			}
			if ValidateSelection(d, sel, now, true) == nil {
				return sel
			}
		}
	}
	t.Fatal("no audit slot")
	return Selection{}
}
func auditRequest(s *Service, method, path, token, key string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("X-Idempotency-Key", key)
	w := httptest.NewRecorder()
	mux := platform.Router(s.Config, s.DB)
	s.Routes(mux)
	mux.ServeHTTP(w, r)
	return w
}
func auditCreate(t *testing.T, s *Service, sel Selection) (Order, string) {
	t.Helper()
	w := auditRequest(s, "POST", "/api/v1/orders", "", uuid.NewString(), map[string]any{"selection": sel, "buyer": Buyer{Name: "Аудит контакт", Phone: "+79000000000"}})
	require.Equal(t, 201, w.Code, w.Body.String())
	var out struct {
		Order Order  `json:"order"`
		Token string `json:"order_token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out.Order, out.Token
}
func auditAdminRequest(s *Service, method, path string, params map[string]string, body any, h http.HandlerFunc) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("X-Idempotency-Key", uuid.NewString())
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	r = r.WithContext(context.WithValue(platform.WithAdmin(r.Context(), platform.Admin{ID: "11111111-1111-4111-8111-111111111111", Role: "owner"}), chi.RouteCtxKey, rc))
	w := httptest.NewRecorder()
	platform.RequireIdempotency(s.Config.InternalKey)(h).ServeHTTP(w, r)
	return w
}
func TestRegressionConcurrentDraftDepartmentMove(t *testing.T) {
	s, _ := auditService(t)
	ctx := context.Background()
	a, b := "b1-male-general", "b2-male-general"
	sel := auditSelection(t, s, a)
	o, token := auditCreate(t, s, sel)
	tx, e := s.DB.Begin(ctx)
	require.NoError(t, e)
	defer func() { _ = tx.Rollback(ctx) }()
	_, e = department(ctx, tx, a, true)
	require.NoError(t, e)
	type result struct {
		tx pgx.Tx
		d  Department
		o  Order
		e  error
	}
	done := make(chan result, 1)
	go func() { x, d, v, e := s.lockedOrder(ctx, o.ID, token, false); done <- result{x, d, v, e} }()
	require.Eventually(t, func() bool {
		var yes bool
		e := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT id,bath_id,%FOR UPDATE')").Scan(&yes)
		return e == nil && yes
	}, 3*time.Second, 10*time.Millisecond)
	_, e = department(ctx, tx, b, true)
	require.NoError(t, e)
	sel = auditSelection(t, s, b)
	_, e = tx.Exec(ctx, "UPDATE orders SET department_id=$2,selection=$3,version=version+1 WHERE id=$1", o.ID, b, bytesJSON(sel))
	require.NoError(t, e)
	require.NoError(t, tx.Commit(ctx))
	got := <-done
	require.Error(t, got.e)
	require.Nil(t, got.tx)
	var api *platform.APIError
	require.ErrorAs(t, got.e, &api)
	require.Equal(t, "department_changed", api.Code)
}
func TestRegressionContactReceiptExpiresAndPurges(t *testing.T) {
	s, _ := auditService(t)
	ctx := context.Background()
	o, token := auditCreate(t, s, auditSelection(t, s, "b1-male-general"))
	_, e := s.DB.Exec(ctx, "UPDATE orders SET created_at=clock_timestamp()-interval '23 hours',access_expires_at=clock_timestamp()+interval '1 hour',contact_purge_at=clock_timestamp()+interval '1 hour' WHERE id=$1", o.ID)
	require.NoError(t, e)
	key := uuid.NewString()
	w := auditRequest(s, "PATCH", "/api/v1/orders/"+o.ID, token, key, map[string]any{"version": 1, "buyer": Buyer{Name: "Аудит изменённый контакт", Phone: "+79000000000"}})
	require.Equal(t, 200, w.Code, w.Body.String())
	var sealed []byte
	var overrun float64
	e = s.DB.QueryRow(ctx, "SELECT response,extract(epoch from (r.expires_at-o.contact_purge_at)) FROM api_requests r JOIN orders o ON o.id=$1 WHERE scope=$2 AND key_hash=$3", o.ID, "order:"+o.ID+":"+platform.Hash(token), platform.Hash(key)).Scan(&sealed, &overrun)
	require.NoError(t, e)
	_, e = s.DB.Exec(ctx, "UPDATE orders SET access_expires_at=clock_timestamp()-interval '1 second',contact_purge_at=clock_timestamp()-interval '1 second' WHERE id=$1", o.ID)
	require.NoError(t, e)
	require.NoError(t, s.PurgeContacts(ctx))
	var buyer string
	require.NoError(t, s.DB.QueryRow(ctx, "SELECT buyer::text FROM orders WHERE id=$1", o.ID).Scan(&buyer))
	require.Equal(t, "{}", buyer)
	require.LessOrEqual(t, overrun, float64(0))
	require.NotEmpty(t, sealed)
	var n int
	require.NoError(t, s.DB.QueryRow(ctx, "SELECT count(*) FROM api_requests WHERE order_id=$1", o.ID).Scan(&n))
	require.Zero(t, n)
}
func TestRegressionResourceActiveLimit(t *testing.T) {
	s, _ := auditService(t)
	ctx := context.Background()
	id := "b1-male-general"
	var n int
	require.NoError(t, s.DB.QueryRow(ctx, "SELECT count(*) FROM resources WHERE department_id=$1 AND active", id).Scan(&n))
	_, e := s.DB.Exec(ctx, "INSERT INTO resources(id,department_id,label,x,y) SELECT gen_random_uuid()::text,$1,'audit-cap-'||g,0,0 FROM generate_series(1,$2::int) g", id, 500-n)
	require.NoError(t, e)
	rid := uuid.NewString()
	_, e = s.DB.Exec(ctx, "INSERT INTO resources(id,department_id,label,x,y,active) VALUES($1,$2,'audit-inactive',0,0,false)", rid, id)
	require.NoError(t, e)
	w := auditAdminRequest(s, "PATCH", "/", map[string]string{"departmentID": id, "resourceID": rid}, map[string]any{"version": 1, "active": true}, s.patchResource)
	require.Equal(t, 409, w.Code, w.Body.String())
	require.NoError(t, s.DB.QueryRow(ctx, "SELECT count(*) FROM resources WHERE department_id=$1 AND active", id).Scan(&n))
	require.Equal(t, 500, n)
}
func TestRegressionBlocksPagination(t *testing.T) {
	s, _ := auditService(t)
	ctx := context.Background()
	dep := "b2-male-general"
	var resource string
	require.NoError(t, s.DB.QueryRow(ctx, "SELECT id FROM resources WHERE department_id=$1 LIMIT 1", dep).Scan(&resource))
	_, e := s.DB.Exec(ctx, "INSERT INTO resource_blocks(id,department_id,resource_ids,starts_at,ends_at,reason) SELECT gen_random_uuid(),$1,ARRAY[$2::text],clock_timestamp()+g*interval '2 hours',clock_timestamp()+g*interval '2 hours'+interval '1 hour','audit' FROM generate_series(1,101) g", dep, resource)
	require.NoError(t, e)
	a := auditAdminRequest(s, "GET", "/?department_id="+dep+"&limit=100", nil, nil, s.listBlocks)
	b := auditAdminRequest(s, "GET", "/?department_id="+dep+"&offset=100", nil, nil, s.listBlocks)
	require.Equal(t, 200, a.Code)
	require.NotEqual(t, a.Body.String(), b.Body.String())
	var body struct {
		Items []Block `json:"items"`
	}
	require.NoError(t, json.Unmarshal(a.Body.Bytes(), &body))
	require.Len(t, body.Items, 100)
	require.NoError(t, json.Unmarshal(b.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
}
func TestRegressionCalendarQueryAndAllocation(t *testing.T) {
	s, tr := auditService(t)
	ctx := context.Background()
	var id string
	require.NoError(t, s.DB.QueryRow(ctx, "SELECT id FROM departments WHERE mode='sauna' ORDER BY id LIMIT 1").Scan(&id))
	now, e := nowDB(ctx, s.DB)
	require.NoError(t, e)
	date := dayStart(now).AddDate(0, 0, 1).Format("2006-01-02")
	for _, step := range []int{30, 1} {
		_, e = s.DB.Exec(ctx, "UPDATE departments SET min_duration_minutes=$2,duration_step_minutes=$2,start_step_minutes=$3 WHERE id=$1", id, map[int]int{30: 60, 1: 1}[step], step)
		require.NoError(t, e)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		n := tr.n.Load()
		r := httptest.NewRequest("GET", "/?from="+date+"&to="+date, nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("departmentID", id)
		c, cancel := context.WithTimeout(context.WithValue(r.Context(), chi.RouteCtxKey, rc), 8*time.Second)
		w := httptest.NewRecorder()
		s.calendar(w, r.WithContext(c))
		cancel()
		runtime.ReadMemStats(&after)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.LessOrEqual(t, tr.n.Load()-n, int64(4))
		t.Logf("step=%d minutes: queries=%d elapsed=%s total allocated=%d bytes, response=%d bytes", step, tr.n.Load()-n, time.Since(start), after.TotalAlloc-before.TotalAlloc, w.Body.Len())
	}
}
func auditTicket(large bool) Ticket {
	d := sauna()
	q, _ := Calculate(d, visit(), []Resource{{ID: "1", Label: "1"}})
	q.BathName = "Сандуновские бани"
	q.BathAddress = "Екатеринбург, Тестовая, 1"
	q.DepartmentName = "Сауна"
	q.Guests = []Guest{{Age: 18, Count: 1}}
	if large {
		q.Mode = "session"
		q.Resources = nil
		q.Guests = nil
		for i := range 100 {
			q.Resources = append(q.Resources, Resource{ID: fmt.Sprint(i), Label: strings.Repeat("М", 80)})
			q.Guests = append(q.Guests, Guest{Age: 18, Count: 1})
		}
	}
	return Ticket{OrderID: "11111111-1111-4111-8111-111111111111", Visit: q, PaidAt: visit().StartsAt, Warning: ticketWarning, TestMode: true}
}
func BenchmarkRegressionTicketPDF(b *testing.B) {
	for _, large := range []bool{false, true} {
		b.Run(fmt.Sprint("large=", large), func(b *testing.B) {
			ticket := auditTicket(large)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				data, e := ticketPDF(ticket)
				if e != nil {
					b.Fatal(e)
				}
				b.ReportMetric(float64(len(data)), "pdf-bytes")
			}
		})
	}
}
func TestRegressionPDFRetainedHeap(t *testing.T) {
	ticket := auditTicket(false)
	for range 20 {
		_, e := ticketPDF(ticket)
		require.NoError(t, e)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	g := runtime.NumGoroutine()
	for range 100 {
		_, e := ticketPDF(ticket)
		require.NoError(t, e)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("100 sequential PDFs: post-GC heap delta=%d bytes, goroutines %d -> %d", int64(after.HeapAlloc)-int64(before.HeapAlloc), g, runtime.NumGoroutine())
	require.Less(t, int64(after.HeapAlloc)-int64(before.HeapAlloc), int64(8<<20), "retained heap must remain bounded after GC")
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= g+2 }, time.Second, 10*time.Millisecond, "PDF generation must not retain workers")
}

func TestRegressionSchedulePreviewBounded(t *testing.T) {
	s, _ := auditService(t)
	ctx := context.Background()
	id := "preview-" + uuid.NewString()
	_, err := s.DB.Exec(ctx, `INSERT INTO departments(id,bath_id,bath_name,bath_address,name,mode,base_guests,max_guests,schedule,tariffs)
 SELECT $1,bath_id,bath_name,bath_address,name,mode,base_guests,max_guests,schedule,tariffs FROM departments WHERE id='b1-male-general'`, id)
	require.NoError(t, err)
	sel := auditSelection(t, s, "b1-male-general")
	sel.DepartmentID = id
	_, err = s.DB.Exec(ctx, `WITH input AS MATERIALIZED (SELECT gen_random_uuid() AS id FROM generate_series(1,257))
 INSERT INTO orders(id,department_id,status,access_hash,idempotency_hash,request_hash,selection,buyer,quote,amount_kopecks,starts_at,ends_at,held_at,expires_at)
 SELECT id,$1,'held','','preview-'||id::text,'',$2,'{}'::jsonb,$3,10000,$4,$5,clock_timestamp(),clock_timestamp()+interval '10 minutes' FROM input`, id, bytesJSON(sel), bytesJSON(Quote{Gender: "mixed"}), sel.StartsAt, sel.EndsAt)
	require.NoError(t, err)
	w := auditAdminRequest(s, "POST", "/api/v1/admin/departments/"+id+"/schedule-preview", map[string]string{"departmentID": id}, map[string]any{"schedule": Schedule{Week: map[string][]Slot{}}}, s.previewSchedule)
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		CanPublish bool              `json:"can_publish"`
		Count      int               `json:"conflict_count"`
		Truncated  bool              `json:"conflicts_truncated"`
		Conflicts  []json.RawMessage `json:"conflicts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.False(t, result.CanPublish)
	require.Equal(t, 257, result.Count)
	require.True(t, result.Truncated)
	require.Len(t, result.Conflicts, 100)
	var remaining int
	require.NoError(t, s.DB.QueryRow(ctx, "SELECT count(*) FROM orders WHERE department_id=$1 AND status='held'", id).Scan(&remaining))
	require.Equal(t, 257, remaining, "preview must never cancel conflicting orders")
}

func TestRegressionAdminOrderSummaryQueryCount(t *testing.T) {
	s, tr := auditService(t)
	ctx := context.Background()
	prototype, _ := auditCreate(t, s, auditSelection(t, s, "b2-male-general"))
	rows, e := s.DB.Query(ctx, "INSERT INTO orders(id,department_id,status,access_hash,idempotency_hash,request_hash,selection,buyer,quote,amount_kopecks,starts_at,ends_at) SELECT gen_random_uuid(),department_id,status,access_hash,gen_random_uuid()::text,request_hash,selection,buyer,quote,amount_kopecks,starts_at,ends_at FROM orders CROSS JOIN generate_series(1,100) WHERE id=$1 RETURNING id::text", prototype.ID)
	require.NoError(t, e)
	orders := []Order{}
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		orders = append(orders, Order{ID: id})
	}
	require.NoError(t, rows.Err())
	rows.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		platform.JSON(w, 200, map[string]any{"items": map[string]any{}})
	}))
	defer upstream.Close()
	s.Config.PaymentURL = upstream.URL
	n := tr.n.Load()
	require.NoError(t, s.paymentSummaries(httptest.NewRequest("GET", "/", nil), orders))
	require.LessOrEqual(t, tr.n.Load()-n, int64(4))
	t.Logf("100 orders without attempts: %d booking SQL calls (including transaction begin/commit)", tr.n.Load()-n)

	attempts := map[string]PaymentAttempt{}
	for i, o := range orders {
		p := PaymentAttempt{ID: uuid.NewString(), OrderID: o.ID, Amount: prototype.Amount, Currency: "RUB", Status: "pending"}
		attempts[o.ID] = p
		if i == 0 {
			_, err := s.DB.Exec(ctx, "INSERT INTO payment_decisions(payment_id,order_id,status,reason) VALUES($1,$2,'rejected','audit')", p.ID, o.ID)
			require.NoError(t, err)
		}
	}
	withAttempts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { platform.JSON(w, 200, map[string]any{"items": attempts}) }))
	defer withAttempts.Close()
	s.Config.PaymentURL = withAttempts.URL
	n = tr.n.Load()
	require.NoError(t, s.paymentSummaries(httptest.NewRequest("GET", "/", nil), orders))
	require.LessOrEqual(t, tr.n.Load()-n, int64(4))
	require.Equal(t, "rejected", orders[0].Payment.Status)
	require.Equal(t, "pending", orders[1].Payment.Status)
	t.Logf("100 orders with attempts: %d booking SQL calls", tr.n.Load()-n)
}

func TestRegressionHistoryCursorDatabaseCollation(t *testing.T) {
	s, _ := auditService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rows, e := s.DB.Query(ctx, "WITH history(id) AS (VALUES ('catalog:1'),('catalog-info:1')) SELECT id FROM history ORDER BY id COLLATE \"C\" DESC LIMIT 2")
	require.NoError(t, e)
	items := []platform.HistoryEntry{}
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		items = append(items, platform.HistoryEntry{ID: id, CreatedAt: now})
	}
	require.NoError(t, rows.Err())
	rows.Close()
	w := httptest.NewRecorder()
	platform.HistoryResponse(w, items, 2)
	var page struct {
		Items []platform.HistoryEntry `json:"items"`
		Next  string                  `json:"next_before_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	rows, e = s.DB.Query(ctx, "WITH history(id) AS (VALUES ('catalog:1'),('catalog-info:1')) SELECT id FROM history WHERE id COLLATE \"C\"<$1 ORDER BY id COLLATE \"C\" DESC LIMIT 2", page.Next)
	require.NoError(t, e)
	defer rows.Close()
	duplicates := 0
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		for _, x := range page.Items {
			if x.ID == id {
				duplicates++
			}
		}
	}
	require.NoError(t, rows.Err())
	require.Zero(t, duplicates)
}

func TestRegressionHoldQuotaAndSnapshot(t *testing.T) {
	s, _ := auditService(t)
	s.Config.MaxActiveHoldsPerClient = 2
	s.Config.MaxHeldResourcesPerClient = 2
	ctx := context.Background()
	dep := "b1-female-general"
	sel := auditSelection(t, s, dep)
	rs, err := resources(ctx, s.DB, dep)
	require.NoError(t, err)
	orders := []Order{}
	tokens := []string{}
	for i := range 3 {
		sel.ResourceIDs = []string{rs[i].ID}
		o, token := auditCreate(t, s, sel)
		orders = append(orders, o)
		tokens = append(tokens, token)
		w := auditRequest(s, "POST", "/api/v1/orders/"+o.ID+"/hold", token, uuid.NewString(), struct{}{})
		if i < 2 {
			require.Equal(t, 200, w.Code, w.Body.String())
		} else {
			require.Equal(t, 429, w.Code, w.Body.String())
		}
	}
	d, err := department(ctx, s.DB, dep, false)
	require.NoError(t, err)
	now, err := nowDB(ctx, s.DB)
	require.NoError(t, err)
	snap, err := s.loadAvailability(ctx, d, sel.StartsAt, spanEnd(d, sel), now)
	require.NoError(t, err)
	legacy, err := availability(ctx, s.DB, d, sel.StartsAt, spanEnd(d, sel))
	require.NoError(t, err)
	for _, resource := range legacy {
		require.Equal(t, resource.State, snap.state(resource.ID, sel.StartsAt, spanEnd(d, sel)))
	}
	w := auditRequest(s, "POST", "/api/v1/orders/"+orders[0].ID+"/release", tokens[0], uuid.NewString(), struct{}{})
	require.Equal(t, 200, w.Code, w.Body.String())
	w = auditRequest(s, "POST", "/api/v1/orders/"+orders[2].ID+"/hold", tokens[2], uuid.NewString(), struct{}{})
	require.Equal(t, 200, w.Code, w.Body.String())
	// Delivery retention must not remove the durable business history.
	_, err = s.DB.Exec(ctx, "UPDATE outbox SET delivered_at=clock_timestamp()-interval '31 days' WHERE aggregate_id=ANY($1::text[])", []string{orders[0].ID, orders[1].ID, orders[2].ID})
	require.NoError(t, err)
	require.NoError(t, platform.CleanupDelivery(ctx, s.DB, s.Config))
	var n int
	require.NoError(t, s.DB.QueryRow(ctx, "SELECT count(*) FROM order_history WHERE order_id=$1 AND action IN ('order.held','order.released')", orders[0].ID).Scan(&n))
	require.Equal(t, 2, n)
}
func TestRegressionPDFCache(t *testing.T) {
	s := &Service{}
	ticket := auditTicket(false)
	ctx := context.Background()
	data, err := s.cachedPDF(ctx, ticket)
	require.NoError(t, err)
	if dir := os.Getenv("BANYA_PDF_QA_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "ticket-normal.pdf"), data, 0o600))
		large, err := ticketPDF(auditTicket(true))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "ticket-large.pdf"), large, 0o600))
	}
	again, err := s.cachedPDF(ctx, ticket)
	require.NoError(t, err)
	require.Equal(t, &data[0], &again[0])
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.cachedPDF(stopped, ticket)
	require.ErrorIs(t, err, context.Canceled)
	cache := newPDFCache(1, 1)
	cache.put("one", make([]byte, 700000))
	cache.put("two", make([]byte, 700000))
	require.Nil(t, cache.get("one", time.Now()))
	require.LessOrEqual(t, cache.bytes, cache.budget)
	require.Nil(t, cache.get("two", time.Now().Add(6*time.Minute)))
	require.Zero(t, cache.bytes)
	s.pdfs.slots <- struct{}{}
	s.pdfs.slots <- struct{}{}
	ticket.OrderID = uuid.NewString()
	_, err = s.cachedPDF(ctx, ticket)
	require.Error(t, err)
	<-s.pdfs.slots
	<-s.pdfs.slots
}
func BenchmarkRegressionCachedPDF(b *testing.B) {
	s := &Service{}
	t := auditTicket(false)
	ctx := context.Background()
	if _, err := s.cachedPDF(ctx, t); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := s.cachedPDF(ctx, t); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRegressionRedisRateLimit(t *testing.T) {
	url := os.Getenv("BANYA_REGRESSION_REDIS_URL")
	if url == "" {
		t.Skip("local regression Redis required")
	}
	rc, err := platform.Redis(url)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	c := platform.Config{InternalKey: uuid.NewString(), PublicRatePerMinute: 2, WriteRatePerMinute: 2, HeavyRatePerMinute: 2}
	handler := platform.ClientAddressMiddleware(c)(platform.RateLimit(c, rc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	keys := []string{}
	defer func() {
		if len(keys) > 0 {
			require.NoError(t, rc.Del(context.Background(), keys...).Err())
		}
	}()
	for _, ip := range []string{"198.18.0.1", "198.18.0.2"} {
		for i := range 3 {
			r := httptest.NewRequest("GET", "/api/v1/baths", nil)
			r.RemoteAddr = ip + ":1234"
			r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.19.0.%d", i))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if i < 2 {
				require.Equal(t, 204, w.Code)
			} else {
				require.Equal(t, 429, w.Code)
			}
			if i == 0 {
				key := "http:rate:v1:read:" + platform.ClientKey(r, c.InternalKey)
				keys = append(keys, key)
				ttl, e := rc.TTL(context.Background(), key).Result()
				require.NoError(t, e)
				require.Positive(t, ttl)
				require.LessOrEqual(t, ttl, time.Minute)
			}
		}
	}
	down := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond, MaxRetries: -1})
	defer func() { _ = down.Close() }()
	handler = platform.RateLimit(c, down)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/api/v1/baths", 204}, {"POST", "/api/v1/orders", 503}, {"GET", "/api/v1/tickets/x/pdf", 503}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, tc.want, w.Code)
	}
}

func TestRegressionPDFConcurrentCache(t *testing.T) {
	s := &Service{}
	ticket := auditTicket(false)
	result := make(chan error, 16)
	for range 16 {
		go func() { _, err := s.cachedPDF(context.Background(), ticket); result <- err }()
	}
	successes := 0
	for range 16 {
		err := <-result
		if err == nil {
			successes++
			continue
		}
		var api *platform.APIError
		require.ErrorAs(t, err, &api)
		require.Equal(t, "pdf_busy", api.Code)
	}
	require.Positive(t, successes)
	s.pdfs.mu.Lock()
	defer s.pdfs.mu.Unlock()
	require.Equal(t, 1, len(s.pdfs.entries))
	require.LessOrEqual(t, s.pdfs.bytes, s.pdfs.budget)
}

func TestRegressionBusyRangeBudget(t *testing.T) {
	s, _ := auditService(t)
	ctx := context.Background()
	sel := auditSelection(t, s, "b2-female-general")
	d, err := department(ctx, s.DB, sel.DepartmentID, false)
	require.NoError(t, err)
	_, err = s.DB.Exec(ctx, "INSERT INTO resource_blocks(id,department_id,resource_ids,starts_at,ends_at,reason) SELECT gen_random_uuid(),$1,ARRAY[$2::text],$3::timestamptz,$4::timestamptz,'range-budget' FROM generate_series(1,100001)", d.ID, sel.ResourceIDs[0], sel.StartsAt, sel.EndsAt)
	require.NoError(t, err)
	_, err = s.loadAvailability(ctx, d, sel.StartsAt, sel.EndsAt, time.Now())
	var api *platform.APIError
	require.ErrorAs(t, err, &api)
	require.Equal(t, "availability_limit", api.Code)
}
func TestRegressionSnapshotBoundaries(t *testing.T) {
	start := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	span := func(a, b time.Duration) pgtype.Multirange[pgtype.Range[time.Time]] {
		return pgtype.Multirange[pgtype.Range[time.Time]]{{Lower: start.Add(a * time.Hour), Upper: start.Add(b * time.Hour), LowerType: pgtype.Inclusive, UpperType: pgtype.Exclusive, Valid: true}}
	}
	snap := availabilitySnapshot{busy: map[string]map[string]pgtype.Multirange[pgtype.Range[time.Time]]{"one": {"held": span(0, 3), "booked": span(0, 1), "blocked": span(1, 2)}}}
	for _, tc := range []struct {
		a, b time.Duration
		want string
	}{{-1, 0, "free"}, {0, 1, "booked"}, {1, 2, "blocked"}, {2, 3, "held"}, {3, 4, "free"}} {
		require.Equal(t, tc.want, snap.state("one", start.Add(tc.a*time.Hour), start.Add(tc.b*time.Hour)))
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	d := sauna()
	sel := visit()
	err := visitCandidates(stopped, d, dayStart(sel.StartsAt), 60, sel.StartsAt.Add(-time.Hour), "one", func(Selection, string) {})
	require.ErrorIs(t, err, context.Canceled)
}
