package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type employeeView struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Version int64  `json:"version"`
	Active  bool   `json:"active"`
}

func TestEmployeeManagement(t *testing.T) {
	s := newSuite(t)
	s.login()
	var me struct {
		Employee employeeView `json:"employee"`
		Session  string       `json:"session_id"`
	}
	s.call("GET", "/api/v1/admin/me", s.admin, "", nil, 200, &me)
	require.Equal(t, "owner", me.Employee.Role)
	s.call("PATCH", "/api/v1/admin/employees/"+me.Employee.ID, s.admin, "", map[string]any{"version": me.Employee.Version, "active": false}, 409, nil)
	username := "check-" + uuid.NewString()
	password := uuid.NewString()
	input := map[string]any{"username": username, "display_name": "Проверка сотрудника", "password": password, "role": "admin"}
	key := uuid.NewString()
	var e, replay employeeView
	s.call("POST", "/api/v1/admin/employees", s.admin, key, input, 201, &e)
	s.call("POST", "/api/v1/admin/employees", s.admin, key, input, 201, &replay)
	require.Equal(t, e.ID, replay.ID)
	t.Cleanup(func() {
		var list struct {
			Items []employeeView `json:"items"`
		}
		s.call("GET", "/api/v1/admin/employees?limit=100", s.admin, "", nil, 200, &list)
		for _, item := range list.Items {
			if item.ID == e.ID && item.Active {
				s.call("PATCH", "/api/v1/admin/employees/"+e.ID, s.admin, "", map[string]any{"version": item.Version, "active": false}, 200, nil)
			}
		}
	})
	input["username"] = strings.ToUpper(username)
	s.call("POST", "/api/v1/admin/employees", s.admin, "", input, 409, nil)
	login := func(pass string) string {
		var result struct {
			Token string `json:"access_token"`
		}
		s.call("POST", "/api/v1/admin/sessions", "", "", map[string]string{"username": username, "password": pass}, 201, &result)
		return result.Token
	}
	first, second := login(password), login(password)
	s.call("GET", "/api/v1/admin/employees", first, "", nil, 403, nil)
	s.call("DELETE", "/api/v1/admin/sessions/"+me.Session, first, "", nil, 404, nil)
	var sessions struct {
		Items []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"items"`
	}
	s.call("GET", "/api/v1/admin/sessions", second, "", nil, 200, &sessions)
	require.Len(t, sessions.Items, 2)
	var firstPage, secondPage struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	s.call("GET", "/api/v1/admin/sessions?limit=1", second, "", nil, 200, &firstPage)
	s.call("GET", "/api/v1/admin/sessions?limit=1&offset=1", second, "", nil, 200, &secondPage)
	require.Len(t, firstPage.Items, 1)
	require.Len(t, secondPage.Items, 1)
	require.NotEqual(t, firstPage.Items[0].ID, secondPage.Items[0].ID)
	for _, item := range sessions.Items {
		if !item.Current {
			s.call("DELETE", "/api/v1/admin/sessions/"+item.ID, second, "", nil, 204, nil)
		}
	}
	s.call("GET", "/api/v1/admin/me", first, "", nil, 401, nil)
	third := login(password)
	newPassword := uuid.NewString()
	s.call("PUT", "/api/v1/admin/me/password", third, "", map[string]string{"current_password": "wrong-password", "new_password": newPassword}, 403, nil)
	change := map[string]string{"current_password": password, "new_password": newPassword}
	key = uuid.NewString()
	s.call("PUT", "/api/v1/admin/me/password", third, key, change, 200, nil)
	s.call("PUT", "/api/v1/admin/me/password", third, key, change, 200, nil)
	s.call("GET", "/api/v1/admin/me", second, "", nil, 401, nil)
	s.call("GET", "/api/v1/admin/me", third, "", nil, 200, nil)
	fourth := login(newPassword)
	var profile struct {
		Employee employeeView `json:"employee"`
	}
	s.call("GET", "/api/v1/admin/me", fourth, "", nil, 200, &profile)
	s.call("PATCH", "/api/v1/admin/employees/"+e.ID, s.admin, "", map[string]any{"version": profile.Employee.Version, "active": false}, 200, nil)
	s.call("GET", "/api/v1/admin/me", fourth, "", nil, 401, nil)
	s.call("GET", "/api/v1/admin/me", third, "", nil, 401, nil)
}

type managedBath struct {
	ID      int    `json:"id"`
	Version int64  `json:"version"`
	Sync    string `json:"sync_status"`
}
type managedDepartment struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Sync    string `json:"sync_status"`
	Error   string `json:"sync_error"`
}

func (s *suite) waitDepartment(id string) managedDepartment {
	s.t.Helper()
	var d managedDepartment
	require.Eventually(s.t, func() bool {
		s.call("GET", "/api/v1/admin/departments/"+id, s.admin, "", nil, 200, &d)
		return d.Sync == "ready" || d.Sync == "error"
	}, 30*time.Second, 200*time.Millisecond)
	require.Equal(s.t, "ready", d.Sync, d.Error)
	return d
}

func TestManagedBathLifecycle(t *testing.T) {
	s := newSuite(t)
	s.login()
	var bath managedBath
	s.call("POST", "/api/v1/admin/baths", s.admin, "", map[string]any{"name": "Тест API " + uuid.NewString(), "address": "Екатеринбург, Тестовая, 1", "active": true}, 201, &bath)
	bathPath := fmt.Sprintf("/api/v1/admin/baths/%d", bath.ID)
	t.Cleanup(func() {
		s.call("GET", bathPath, s.admin, "", nil, 200, &bath)
		code, _, err := s.raw("PATCH", bathPath, s.admin, "", map[string]any{"version": bath.Version, "active": false})
		require.NoError(t, err)
		require.Contains(t, []int{200, 202}, code)
	})
	week := map[string]any{}
	for day := range 7 {
		week[fmt.Sprint(day)] = []map[string]string{{"start": "10:00", "end": "11:00", "gender": "mixed"}, {"start": "14:00", "end": "15:00", "gender": "mixed"}}
	}
	schedule := map[string]any{"week": week, "exceptions": map[string]any{}, "calendar": map[string]any{}}
	tariffs := map[string]any{"rules": []map[string]any{{"id": "test", "valid_from": "2020-01-01", "valid_until": "2100-01-01", "day_types": []string{"weekday", "weekend", "holiday"}, "start_minute": 0, "end_minute": 1440, "duration_minutes": 60, "adult_kopecks": 100000, "child_kopecks": 50000, "hourly_kopecks": 0, "extra_hourly_kopecks": 0}}}
	setup := map[string]any{"base_guests": 1, "max_guests": 20, "min_duration_minutes": 60, "duration_step_minutes": 60, "start_step_minutes": 30, "buffer_minutes": 0, "schedule": schedule, "tariffs": tariffs, "resources": []map[string]any{{"label": "1", "x": 10, "y": 20}, {"label": "2", "x": 40, "y": 20}}}
	input := map[string]any{"name": "Новое отделение", "mode": "session", "active": true, "booking_setup": setup}
	var d, replay managedDepartment
	key := uuid.NewString()
	s.call("POST", bathPath+"/departments", s.admin, key, input, 202, &d)
	s.call("POST", bathPath+"/departments", s.admin, key, input, 202, &replay)
	require.Equal(t, d.ID, replay.ID)
	d = s.waitDepartment(d.ID)
	var info managedDepartment
	s.call("POST", bathPath+"/departments", s.admin, "", map[string]any{"name": "Справочное", "description": "Посещение по месту", "mode": "info", "active": true}, 202, &info)
	s.waitDepartment(info.ID)
	s.call("GET", "/api/v1/departments/"+info.ID, "", "", nil, 200, nil)
	s.call("GET", "/api/v1/departments/"+info.ID+"/resources", "", "", nil, 404, nil)
	var sauna managedDepartment
	saunaSetup := map[string]any{}
	for k, value := range setup {
		saunaSetup[k] = value
	}
	saunaSetup["buffer_minutes"] = 30
	saunaSetup["resources"] = []map[string]any{{"label": "Сауна целиком", "x": 100, "y": 100}}
	saunaSetup["tariffs"] = map[string]any{"rules": []map[string]any{{"id": "test-sauna", "valid_from": "2020-01-01", "valid_until": "2100-01-01", "day_types": []string{"weekday", "weekend", "holiday"}, "start_minute": 0, "end_minute": 1440, "duration_minutes": 0, "adult_kopecks": 0, "child_kopecks": 0, "hourly_kopecks": 100000, "extra_hourly_kopecks": 10000}}}
	s.call("POST", bathPath+"/departments", s.admin, "", map[string]any{"name": "Новая сауна", "mode": "sauna", "active": true, "booking_setup": saunaSetup}, 202, &sauna)
	s.waitDepartment(sauna.ID)
	s.create(s.find(sauna.ID, 1))
	adminPath := "/api/v1/admin/departments/" + d.ID
	publicPath := "/api/v1/departments/" + d.ID
	s.call("GET", publicPath, "", "", nil, 200, nil)
	var layout struct {
		Version   int64 `json:"version"`
		Resources []struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		} `json:"resources"`
	}
	s.call("GET", adminPath+"/resources", s.admin, "", nil, 200, &layout)
	require.Len(t, layout.Resources, 2)
	var added struct {
		Resource struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		} `json:"resource"`
	}
	s.call("POST", adminPath+"/resources", s.admin, "", map[string]any{"scheme_version": layout.Version, "label": "3", "x": 100, "y": 100}, 201, &added)
	s.call("PATCH", adminPath+"/resources/"+added.Resource.ID, s.admin, "", map[string]any{"version": added.Resource.Version, "active": false}, 200, nil)
	s.call("GET", publicPath+"/resources", "", "", nil, 200, &layout)
	require.Len(t, layout.Resources, 2)
	var policy map[string]any
	s.call("GET", adminPath+"/booking-policy", s.admin, "", nil, 200, &policy)
	s.call("PATCH", adminPath+"/booking-policy", s.admin, "", map[string]any{"version": policy["version"], "max_guests": 15}, 200, &policy)
	s.call("PATCH", adminPath+"/booking-policy", s.admin, "", map[string]any{"version": 1, "max_guests": 14}, 409, nil)
	sel := s.find(d.ID, 1)
	date := sel.StartsAt.In(time.FixedZone("YEKT", 5*3600)).Format("2006-01-02")
	var calendar struct {
		Items []map[string]any `json:"items"`
	}
	s.call("GET", publicPath+"/availability-calendar?from="+date+"&to="+date, "", "", nil, 200, &calendar)
	require.Equal(t, "available", calendar.Items[0]["status"])
	s.call("POST", adminPath+"/schedule-preview", s.admin, "", map[string]any{"schedule": schedule}, 200, nil)
	s.call("POST", adminPath+"/tariff-preview", s.admin, "", map[string]any{"tariffs": tariffs, "selection": sel}, 200, nil)
	c := s.create(sel)
	var before struct {
		Version int64     `json:"version"`
		Access  time.Time `json:"access_expires_at"`
	}
	s.call("GET", "/api/v1/orders/"+c.Order.ID, c.Token, "", nil, 200, &before)
	sel.ResourceIDs = []string{layout.Resources[1].ID}
	patch := map[string]any{"version": before.Version, "selection": sel, "buyer": map[string]string{"name": "Исправленный контакт", "phone": "+79000000001"}}
	var after struct {
		Version int64     `json:"version"`
		Access  time.Time `json:"access_expires_at"`
	}
	key = uuid.NewString()
	s.call("PATCH", "/api/v1/orders/"+c.Order.ID, c.Token, key, patch, 200, &after)
	require.Equal(t, before.Access, after.Access)
	require.Equal(t, before.Version+1, after.Version)
	s.call("PATCH", "/api/v1/orders/"+c.Order.ID, c.Token, key, patch, 200, &after)
	s.call("PATCH", "/api/v1/orders/"+c.Order.ID, c.Token, "", patch, 409, nil)
	h := s.hold(c)
	s.call("PATCH", "/api/v1/orders/"+c.Order.ID, c.Token, "", map[string]any{"version": after.Version + 1, "buyer": patch["buyer"]}, 409, nil)
	s.call("PATCH", adminPath+"/resources/"+sel.ResourceIDs[0], s.admin, "", map[string]any{"version": 1, "active": false}, 409, nil)
	closed := map[string]any{"week": map[string]any{}, "exceptions": map[string]any{}, "calendar": map[string]any{}}
	var preview struct {
		Can       bool  `json:"can_publish"`
		Conflicts []any `json:"conflicts"`
	}
	s.call("POST", adminPath+"/schedule-preview", s.admin, "", map[string]any{"schedule": closed}, 200, &preview)
	require.False(t, preview.Can)
	require.NotEmpty(t, preview.Conflicts)
	var payment attempt
	s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, "", paymentBody(h, "success"), 202, &payment)
	s.waitAttempt(c, payment, "accepted")
	s.call("GET", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, "", nil, 200, nil)
	s.call("GET", "/api/v1/admin/orders/"+c.Order.ID+"/payments", s.admin, "", nil, 200, nil)
	o := s.read(c)
	u, err := url.Parse(o.TicketURL)
	require.NoError(t, err)
	code, pdf, err := s.raw("GET", u.Path+"/pdf", "", "", nil)
	require.NoError(t, err)
	require.Equal(t, 200, code)
	require.True(t, strings.HasPrefix(string(pdf), "%PDF-"))
	var history struct {
		Items    []platform.HistoryEntry `json:"items"`
		Before   string                  `json:"next_before"`
		BeforeID string                  `json:"next_before_id"`
	}
	seen := map[string]bool{}
	cursor := ""
	for range 20 {
		s.call("GET", "/api/v1/admin/orders/"+c.Order.ID+"/history?limit=2"+cursor, s.admin, "", nil, 200, &history)
		for _, entry := range history.Items {
			require.False(t, seen[entry.ID], "cursor must not duplicate entries")
			seen[entry.ID] = true
			encoded, e := json.Marshal(entry)
			require.NoError(t, e)
			require.NotContains(t, string(encoded), "+79000000001")
			require.NotContains(t, string(encoded), "Исправленный контакт")
		}
		if len(history.Items) < 2 {
			break
		}
		cursor = "&before=" + url.QueryEscape(history.Before) + "&before_id=" + url.QueryEscape(history.BeforeID)
	}
	require.GreaterOrEqual(t, len(seen), 6)
	// Duplicate and stale metadata cannot roll back booking-owned policy/resources.
	s.call("PATCH", adminPath, s.admin, "", map[string]any{"version": d.Version, "name": "Обновлённое отделение"}, 202, nil)
	d = s.waitDepartment(d.ID)
	event := platform.Event{ID: uuid.NewString(), Type: "catalog.department.updated", Version: 1, AggregateID: d.ID, OccurredAt: time.Now(), Data: json.RawMessage(fmt.Sprintf(`{"id":%q,"bath_id":%d,"bath_name":"Old","bath_address":"Old","name":"Stale","mode":"session","active":true,"version":1}`, d.ID, bath.ID))}
	s.publish(event)
	s.publish(event)
	require.Eventually(t, func() bool {
		var count int
		err := s.db.QueryRow(context.Background(), "SELECT count(*) FROM inbox WHERE id=$1", event.ID).Scan(&count)
		return err == nil && count == 1
	}, 20*time.Second, 200*time.Millisecond)
	var projected struct {
		Name string `json:"name"`
		Max  int    `json:"max_guests"`
	}
	s.call("GET", publicPath+"/booking-config", "", "", nil, 200, &projected)
	require.Equal(t, "Обновлённое отделение", projected.Name)
	require.Equal(t, 15, projected.Max)
	s.call("GET", adminPath+"/history", s.admin, "", nil, 200, &history)
	require.NotEmpty(t, history.Items)
	// Archiving preserves the confirmed order and applies a versioned booking barrier.
	s.call("PATCH", adminPath, s.admin, "", map[string]any{"version": d.Version, "active": false}, 202, nil)
	s.waitDepartment(d.ID)
	s.call("POST", "/api/v1/quotes", "", "", sel, 409, nil)
	require.Equal(t, "confirmed", s.read(c).Status)
	var allocations int
	require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM allocations WHERE order_id=$1 AND active", c.Order.ID).Scan(&allocations))
	require.Equal(t, 1, allocations)
}
