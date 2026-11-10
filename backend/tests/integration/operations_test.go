package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOperationalAdministration(t *testing.T) {
	s := newSuite(t)
	s.login()
	var bath managedBath
	s.call("POST", "/api/v1/admin/baths", s.admin, "", map[string]any{"name": "Операционные проверки " + uuid.NewString(), "address": "Синтетические данные", "active": true}, 201, &bath)
	bathPath := fmt.Sprintf("/api/v1/admin/baths/%d", bath.ID)
	t.Cleanup(func() {
		s.call("GET", bathPath, s.admin, "", nil, 200, &bath)
		s.call("PATCH", bathPath, s.admin, "", map[string]any{"version": bath.Version, "active": false}, 202, nil)
	})
	week := map[string]any{}
	for day := range 7 {
		week[fmt.Sprint(day)] = []map[string]string{{"start": "10:00", "end": "11:00", "gender": "mixed"}, {"start": "14:00", "end": "15:00", "gender": "mixed"}}
	}
	setup := map[string]any{"base_guests": 1, "max_guests": 20, "min_duration_minutes": 60, "duration_step_minutes": 60, "start_step_minutes": 30, "buffer_minutes": 0, "schedule": map[string]any{"week": week, "exceptions": map[string]any{}, "calendar": map[string]any{}}, "tariffs": map[string]any{"rules": []map[string]any{{"id": "test", "valid_from": "2020-01-01", "valid_until": "2100-01-01", "day_types": []string{"weekday", "weekend", "holiday"}, "start_minute": 0, "end_minute": 1440, "duration_minutes": 60, "adult_kopecks": 100000, "child_kopecks": 50000, "hourly_kopecks": 0, "extra_hourly_kopecks": 0}}}, "resources": []map[string]any{{"label": "1", "x": 10, "y": 10}, {"label": "2", "x": 20, "y": 20}, {"label": "3", "x": 30, "y": 30}}}
	var dep managedDepartment
	s.call("POST", bathPath+"/departments", s.admin, "", map[string]any{"name": "Операционные проверки", "mode": "session", "active": true, "booking_setup": setup}, 202, &dep)
	dep = s.waitDepartment(dep.ID)
	adminPath := "/api/v1/admin/departments/" + dep.ID
	publicPath := "/api/v1/departments/" + dep.ID
	s.call("POST", adminPath+"/schedule-preview", s.admin, "", map[string]any{"schedule": map[string]any{"week": map[string]any{"1": []map[string]string{{"start": "+1:00", "end": "02:00", "gender": "mixed"}}}}}, 400, nil)
	sel := s.find(dep.ID, 3)
	ids := append([]string(nil), sel.ResourceIDs...)
	sel.ResourceIDs = ids[:1]
	sel.Guests = []map[string]any{{"category": "adult", "count": 1}}
	create := func(selection selection) created {
		var c created
		s.call("POST", "/api/v1/orders", "", "", map[string]any{"selection": selection, "buyer": map[string]string{"name": "private-buyer-ops-marker", "phone": "+79991234567"}}, 201, &c)
		return c
	}
	c := create(sel)
	held := s.hold(c)
	secondSel := sel
	secondSel.ResourceIDs = ids[1:2]
	draft := create(secondSel)
	var policy map[string]any
	s.call("GET", adminPath+"/booking-policy", s.admin, "", nil, 200, &policy)
	s.call("GET", publicPath+"/booking-config", "", "", nil, 200, nil) // prime the public cache
	pauseBody := map[string]any{"version": policy["version"], "online_sales_enabled": false, "sales_pause_reason": "Плановые работы", "sales_paused_until": nil}
	pauseKey := uuid.NewString()
	s.call("PATCH", adminPath+"/booking-policy", s.admin, pauseKey, pauseBody, 200, &policy)
	require.Equal(t, false, policy["online_sales_enabled"])
	s.call("PATCH", adminPath+"/booking-policy", s.admin, pauseKey, pauseBody, 200, nil)
	s.call("PATCH", adminPath+"/booking-policy", s.admin, "", pauseBody, 409, nil)
	var published map[string]any
	s.call("GET", publicPath+"/booking-config", "", "", nil, 200, &published)
	require.Equal(t, false, published["online_sales_enabled"])
	s.call("POST", "/api/v1/orders", "", "", map[string]any{"selection": sel, "buyer": map[string]string{"name": "Blocked", "phone": "+79991234567"}}, 409, nil)
	s.call("POST", "/api/v1/orders/"+draft.Order.ID+"/hold", draft.Token, "", map[string]any{}, 409, nil)
	s.call("POST", "/api/v1/orders/"+c.Order.ID+"/hold", c.Token, "", map[string]any{}, 200, nil)
	var payment attempt
	s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, "", paymentBody(held, "success"), 202, &payment)
	s.waitAttempt(c, payment, "accepted")
	require.Equal(t, "confirmed", s.read(c).Status)
	// A fresh metadata version must preserve booking-owned pause and policy.
	retryBody := map[string]any{"version": dep.Version, "reason": "Проверка восстановления"}
	retryKey := uuid.NewString()
	var retry, replayed managedDepartment
	s.call("POST", adminPath+"/sync-retry", s.admin, retryKey, retryBody, 202, &retry)
	s.call("POST", adminPath+"/sync-retry", s.admin, retryKey, retryBody, 202, &replayed)
	require.Equal(t, retry, replayed)
	require.Equal(t, dep.Version+1, retry.Version)
	s.waitDepartment(dep.ID)
	s.call("GET", adminPath+"/booking-policy", s.admin, "", nil, 200, &policy)
	require.Equal(t, false, policy["online_sales_enabled"])
	s.call("POST", adminPath+"/sync-retry", s.admin, "", retryBody, 409, nil)
	s.call("GET", publicPath+"/booking-config", "", "", nil, 200, &published)
	require.Equal(t, false, published["online_sales_enabled"])
	// Expired pauses resume automatically even while the raw config remains cached.
	s.call("PATCH", adminPath+"/booking-policy", s.admin, "", map[string]any{"version": policy["version"], "sales_paused_until": time.Now().UTC().Add(2 * time.Second)}, 200, &policy)
	s.call("GET", publicPath+"/booking-config", "", "", nil, 200, nil)
	require.Eventually(t, func() bool {
		s.call("GET", publicPath+"/booking-config", "", "", nil, 200, &published)
		return published["online_sales_enabled"] == true
	}, 6*time.Second, 250*time.Millisecond)
	var adminConfig map[string]any
	s.call("GET", "/api/v1/admin/schedules/"+dep.ID, s.admin, "", nil, 200, &adminConfig)
	require.Equal(t, true, adminConfig["online_sales_enabled"])
	require.Nil(t, adminConfig["sales_paused_until"])
	require.Empty(t, adminConfig["sales_pause_reason"])
	active := create(secondSel)
	s.hold(active)
	from, until := sel.StartsAt.Format(time.RFC3339), sel.EndsAt.Format(time.RFC3339)
	var block struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	s.call("POST", "/api/v1/admin/resource-blocks", s.admin, "", map[string]any{"department_id": dep.ID, "resource_ids": ids[2:3], "starts_at": sel.StartsAt, "ends_at": sel.EndsAt, "reason": "Тестовая блокировка"}, 201, &block)
	query := "?from=" + url.QueryEscape(from) + "&until=" + url.QueryEscape(until)
	type busy struct {
		ID         string `json:"id"`
		ResourceID string `json:"resource_id"`
		State      string `json:"state"`
		OrderID    string `json:"order_id"`
	}
	var occupancy struct {
		Items []busy `json:"items"`
	}
	s.call("GET", adminPath+"/occupancy"+query, s.admin, "", nil, 200, &occupancy)
	require.Len(t, occupancy.Items, 3)
	states := map[string]string{}
	for _, item := range occupancy.Items {
		states[item.ResourceID] = item.State
	}
	require.Equal(t, "booked", states[ids[0]])
	require.Equal(t, "held", states[ids[1]])
	require.Equal(t, "blocked", states[ids[2]])
	data, err := json.Marshal(occupancy)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private-buyer")
	require.NotContains(t, string(data), c.Token)
	s.call("GET", adminPath+"/occupancy"+query+"&limit=2", s.admin, "", nil, 200, &occupancy)
	require.Len(t, occupancy.Items, 2)
	first := occupancy.Items[0].ID
	s.call("GET", adminPath+"/occupancy"+query+"&limit=2&offset=2", s.admin, "", nil, 200, &occupancy)
	require.Len(t, occupancy.Items, 1)
	require.NotEqual(t, first, occupancy.Items[0].ID)
	s.call("GET", adminPath+"/occupancy"+query+"&resource_id="+url.QueryEscape(ids[1]), s.admin, "", nil, 200, &occupancy)
	require.Len(t, occupancy.Items, 1)
	require.Equal(t, active.Order.ID, occupancy.Items[0].OrderID)
	s.call("GET", adminPath+"/occupancy"+query, "", "", nil, 401, nil)
	s.call("GET", adminPath+"/occupancy?from=invalid&until=invalid", s.admin, "", nil, 400, nil)
	type searchResult struct {
		Items []struct {
			ID    string         `json:"id"`
			Buyer map[string]any `json:"buyer"`
		} `json:"items"`
	}
	var found searchResult
	searchBody := map[string]any{"phone": "8 (999) 123-45-67", "bath_id": bath.ID, "department_id": dep.ID, "resource_id": ids[0], "status": "confirmed", "from": from, "until": until, "limit": 1}
	key := uuid.NewString()
	s.call("POST", "/api/v1/admin/orders/search", s.admin, key, searchBody, 200, &found)
	require.Len(t, found.Items, 1)
	require.Equal(t, c.Order.ID, found.Items[0].ID)
	s.call("POST", "/api/v1/admin/orders/search", s.admin, key, searchBody, 200, &found)
	var receipts int
	require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM api_requests WHERE key_hash=$1", platform.Hash(key)).Scan(&receipts))
	require.Zero(t, receipts)
	s.call("POST", "/api/v1/admin/orders/search", "", "", searchBody, 401, nil)
	s.call("POST", "/api/v1/admin/orders/search", s.admin, "", map[string]any{"order_id": "invalid"}, 400, nil)
	s.call("POST", "/api/v1/admin/orders/search", s.admin, "", map[string]any{"phone": "invalid"}, 400, nil)
	// A still-stored contact must be unsearchable immediately at its deadline.
	_, err = s.db.Exec(context.Background(), "UPDATE orders SET access_expires_at=clock_timestamp()-interval '1 second',contact_purge_at=clock_timestamp()-interval '1 second' WHERE id=$1", draft.Order.ID)
	require.NoError(t, err)
	s.call("POST", "/api/v1/admin/orders/search", s.admin, "", map[string]any{"phone": "+79991234567", "order_id": draft.Order.ID}, 200, &found)
	require.Empty(t, found.Items)
	s.call("POST", "/api/v1/admin/orders/search", s.admin, "", map[string]any{"order_id": draft.Order.ID}, 200, &found)
	require.Len(t, found.Items, 1)
	require.Empty(t, found.Items[0].Buyer)
	// Expired holds disappear without depending on the expiry worker's next tick.
	_, err = s.db.Exec(context.Background(), "UPDATE orders SET held_at=clock_timestamp()-interval '11 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", active.Order.ID)
	require.NoError(t, err)
	s.call("GET", adminPath+"/occupancy"+query+"&resource_id="+url.QueryEscape(ids[1]), s.admin, "", nil, 200, &occupancy)
	require.Empty(t, occupancy.Items)
	s.release(active)
	// The occupied interval includes the sanitary buffer for a sauna.
	saunaSetup := map[string]any{}
	for key, value := range setup {
		saunaSetup[key] = value
	}
	saunaSetup["buffer_minutes"] = 30
	saunaSetup["resources"] = []map[string]any{{"label": "Сауна", "x": 10, "y": 10}}
	saunaSetup["tariffs"] = map[string]any{"rules": []map[string]any{{"id": "hour", "valid_from": "2020-01-01", "valid_until": "2100-01-01", "day_types": []string{"weekday", "weekend", "holiday"}, "start_minute": 0, "end_minute": 1440, "duration_minutes": 0, "adult_kopecks": 0, "child_kopecks": 0, "hourly_kopecks": 100000, "extra_hourly_kopecks": 10000}}}
	var sauna managedDepartment
	s.call("POST", bathPath+"/departments", s.admin, "", map[string]any{"name": "Сауна проверки буфера", "mode": "sauna", "active": true, "booking_setup": saunaSetup}, 202, &sauna)
	s.waitDepartment(sauna.ID)
	saunaOrder := s.create(s.find(sauna.ID, 1))
	s.hold(saunaOrder)
	bufferStart, bufferEnd := saunaOrder.Order.Selection.EndsAt, saunaOrder.Order.Selection.EndsAt.Add(30*time.Minute)
	bufferPath := "/api/v1/admin/departments/" + sauna.ID + "/occupancy?from=" + url.QueryEscape(bufferStart.Format(time.RFC3339)) + "&until=" + url.QueryEscape(bufferEnd.Format(time.RFC3339))
	s.call("GET", bufferPath, s.admin, "", nil, 200, &occupancy)
	require.Len(t, occupancy.Items, 1)
	s.call("GET", "/api/v1/admin/departments/"+sauna.ID+"/occupancy?from="+url.QueryEscape(bufferEnd.Format(time.RFC3339))+"&until="+url.QueryEscape(bufferEnd.Add(time.Minute).Format(time.RFC3339)), s.admin, "", nil, 200, &occupancy)
	require.Empty(t, occupancy.Items)
	s.release(saunaOrder)
	// Pause and hold serialize on the same department lock.
	candidate := create(secondSel)
	s.call("GET", adminPath+"/booking-policy", s.admin, "", nil, 200, &policy)
	codes := make([]int, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		codes[0], _, errs[0] = s.raw("PATCH", adminPath+"/booking-policy", s.admin, "", map[string]any{"version": policy["version"], "online_sales_enabled": false, "sales_pause_reason": "Проверка гонки"})
	}()
	go func() {
		defer wg.Done()
		<-start
		codes[1], _, errs[1] = s.raw("POST", "/api/v1/orders/"+candidate.Order.ID+"/hold", candidate.Token, "", map[string]any{})
	}()
	close(start)
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.Equal(t, 200, codes[0])
	require.Contains(t, []int{200, 409}, codes[1])
	s.call("GET", adminPath+"/booking-policy", s.admin, "", nil, 200, &policy)
	require.Equal(t, false, policy["online_sales_enabled"])
	s.release(candidate)
	s.call("PATCH", adminPath+"/booking-policy", s.admin, "", map[string]any{"version": policy["version"], "online_sales_enabled": true}, 200, &policy)
	require.Empty(t, policy["sales_pause_reason"])
	require.Nil(t, policy["sales_paused_until"])
	s.call("DELETE", fmt.Sprintf("/api/v1/admin/resource-blocks/%s?version=%d", block.ID, block.Version), s.admin, "", nil, 200, nil)
	// Employee card and management audit are owner-only, and safe under retries.
	var e employeeView
	password := uuid.NewString()
	username := "ops-" + uuid.NewString()
	s.call("POST", "/api/v1/admin/employees", s.admin, "", map[string]any{"username": username, "display_name": "private-employee-marker", "password": password, "role": "admin"}, 201, &e)
	t.Cleanup(func() {
		var current employeeView
		s.call("GET", "/api/v1/admin/employees/"+e.ID, s.admin, "", nil, 200, &current)
		if current.Active {
			s.call("PATCH", "/api/v1/admin/employees/"+e.ID, s.admin, "", map[string]any{"version": current.Version, "active": false}, 200, nil)
		}
	})
	s.call("GET", "/api/v1/admin/employees/"+e.ID, s.admin, "", nil, 200, &e)
	var session struct {
		Token string `json:"access_token"`
	}
	s.call("POST", "/api/v1/admin/sessions", "", "", map[string]string{"username": username, "password": password}, 201, &session)
	s.call("GET", "/api/v1/admin/security-events", session.Token, "", nil, 403, nil)
	s.call("GET", "/api/v1/admin/employees/"+e.ID, session.Token, "", nil, 403, nil)
	s.call("GET", "/api/v1/admin/security-events", "", "", nil, 401, nil)
	resetPassword := uuid.NewString()
	resetBody := map[string]any{"version": e.Version, "role": "owner", "password": resetPassword}
	resetKey := uuid.NewString()
	s.call("PATCH", "/api/v1/admin/employees/"+e.ID, s.admin, resetKey, resetBody, 200, &e)
	s.call("PATCH", "/api/v1/admin/employees/"+e.ID, s.admin, resetKey, resetBody, 200, nil)
	var ownerSession struct {
		Token string `json:"access_token"`
	}
	s.call("POST", "/api/v1/admin/sessions", "", "", map[string]string{"username": username, "password": resetPassword}, 201, &ownerSession)
	for _, alias := range []string{e.ID, strings.ToUpper(e.ID), "urn:uuid:" + e.ID, strings.ReplaceAll(e.ID, "-", "")} {
		s.call("PATCH", "/api/v1/admin/employees/"+alias, ownerSession.Token, "", map[string]any{"version": e.Version, "password": resetPassword}, 400, nil)
	}
	s.call("GET", "/api/v1/admin/me", ownerSession.Token, "", nil, 200, nil)
	s.call("DELETE", "/api/v1/admin/sessions/current", ownerSession.Token, "", nil, 204, nil)
	s.call("PATCH", "/api/v1/admin/employees/"+e.ID, s.admin, "", map[string]any{"version": e.Version, "active": false}, 200, &e)
	seen := map[string]bool{}
	actions := map[string]bool{}
	cursor := ""
	for range 10 {
		var page struct {
			Items    []platform.HistoryEntry `json:"items"`
			Before   string                  `json:"next_before"`
			BeforeID string                  `json:"next_before_id"`
		}
		s.call("GET", "/api/v1/admin/security-events?employee_id="+e.ID+"&limit=2"+cursor, s.admin, "", nil, 200, &page)
		for _, item := range page.Items {
			require.False(t, seen[item.ID])
			seen[item.ID] = true
			actions[item.Action] = true
			raw, err := json.Marshal(item)
			require.NoError(t, err)
			require.NotContains(t, string(raw), password)
			require.NotContains(t, string(raw), "private-employee-marker")
			require.NotContains(t, string(raw), "$2a$")
		}
		if len(page.Items) < 2 {
			break
		}
		cursor = "&before=" + url.QueryEscape(page.Before) + "&before_id=" + url.QueryEscape(page.BeforeID)
	}
	for _, action := range []string{"employee.create", "employee.role_changed", "employee.password_reset", "employee.blocked"} {
		require.True(t, actions[action], action)
	}
	require.Len(t, seen, 4, "replaying PATCH must not duplicate security events")
	s.call("GET", "/api/v1/admin/security-events?employee_id=invalid", s.admin, "", nil, 400, nil)
}
