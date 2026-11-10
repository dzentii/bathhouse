package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPublishedSchedule(t *testing.T) {
	s := newSuite(t)
	s.login()
	sel := s.find("b2-female-general", 1)
	var config map[string]any
	s.call("GET", "/api/v1/admin/schedules/b2-female-general", s.admin, "", nil, 200, &config)
	original := config["schedule"].(map[string]any)
	copyData, _ := json.Marshal(original)
	var proposed map[string]any
	require.NoError(t, json.Unmarshal(copyData, &proposed))
	loc, _ := time.LoadLocation("Asia/Yekaterinburg")
	date := sel.StartsAt.In(loc).Format("2006-01-02")
	proposed["exceptions"].(map[string]any)[date] = []map[string]string{{"start": sel.StartsAt.In(loc).Format("15:04"), "end": sel.EndsAt.In(loc).Format("15:04"), "gender": "male"}}
	version := config["schedule_version"].(float64)
	s.call("PUT", "/api/v1/admin/schedules/b2-female-general", s.admin, "", map[string]any{"version": version, "schedule": proposed}, 200, nil)
	t.Cleanup(func() {
		s.call("PUT", "/api/v1/admin/schedules/b2-female-general", s.admin, "", map[string]any{"version": version + 1, "schedule": original}, 200, nil)
	})
	var availability struct {
		Items []struct {
			Gender string `json:"gender"`
		} `json:"items"`
	}
	s.call("GET", "/api/v1/departments/b2-female-general/availability?date="+date, "", "", nil, 200, &availability)
	require.Len(t, availability.Items, 1)
	require.Equal(t, "male", availability.Items[0].Gender)
}

func TestCookieSessionAndCSRF(t *testing.T) {
	s := newSuite(t)
	body, _ := json.Marshal(map[string]string{"username": s.env["ADMIN_USERNAME"], "password": s.env["ADMIN_PASSWORD"]})
	req, e := http.NewRequest("POST", s.base+"/api/v1/admin/sessions", bytes.NewReader(body))
	require.NoError(t, e)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Idempotency-Key", uuid.NewString())
	res, e := http.DefaultClient.Do(req)
	require.NoError(t, e)
	require.Equal(t, 201, res.StatusCode)
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&session))
	_ = res.Body.Close()
	cookies := res.Cookies()
	require.NotEmpty(t, cookies)
	require.True(t, cookies[0].HttpOnly)
	request := func(method, path, csrf, origin string) int {
		req, e := http.NewRequest(method, s.base+path, nil)
		require.NoError(t, e)
		req.AddCookie(cookies[0])
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response, e := http.DefaultClient.Do(req)
		require.NoError(t, e)
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode
	}
	require.Equal(t, 200, request("GET", "/api/v1/admin/orders", "", ""))
	require.Equal(t, 403, request("DELETE", "/api/v1/admin/sessions/current", "", ""))
	require.Equal(t, 403, request("DELETE", "/api/v1/admin/sessions/current", session.CSRF, "https://untrusted.invalid"))
	require.Equal(t, 204, request("DELETE", "/api/v1/admin/sessions/current", session.CSRF, ""))
	require.Equal(t, 401, request("GET", "/api/v1/admin/orders", "", ""))
}
