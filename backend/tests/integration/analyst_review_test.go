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

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type reviewBlock struct {
	ID          string    `json:"id"`
	Version     int64     `json:"version"`
	Active      bool      `json:"active"`
	ResourceIDs []string  `json:"resource_ids"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
}

func reviewDepartment(s *suite, mode string) managedDepartment {
	var bath managedBath
	s.call("POST", "/api/v1/admin/baths", s.admin, "", map[string]any{"name": "Review " + uuid.NewString(), "address": "Synthetic", "active": true}, 201, &bath)
	path := fmt.Sprintf("/api/v1/admin/baths/%d", bath.ID)
	s.t.Cleanup(func() {
		s.call("GET", path, s.admin, "", nil, 200, &bath)
		status, _, err := s.raw("PATCH", path, s.admin, "", map[string]any{"version": bath.Version, "active": false})
		require.NoError(s.t, err)
		require.Contains(s.t, []int{200, 202}, status)
	})
	week := map[string]any{}
	start, end := "10:00", "11:00"
	duration, hourly, adult := 60, 0, 100000
	buffer := 0
	resources := []map[string]any{{"label": "58", "x": 10, "y": 10}, {"label": "61", "x": 20, "y": 20}}
	if mode == "sauna" {
		start, end = "12:30", "21:30"
		duration, hourly, adult = 0, 100000, 0
		buffer = 30
		resources = resources[:1]
	}
	for day := range 7 {
		week[fmt.Sprint(day)] = []map[string]string{{"start": start, "end": end, "gender": "mixed"}}
	}
	setup := map[string]any{"base_guests": 1, "max_guests": 5, "min_duration_minutes": 60, "duration_step_minutes": 30, "start_step_minutes": 30, "buffer_minutes": buffer, "schedule": map[string]any{"week": week, "exceptions": map[string]any{}, "calendar": map[string]any{}}, "tariffs": map[string]any{"rules": []map[string]any{{"id": "review", "valid_from": "2020-01-01", "valid_until": "2100-01-01", "day_types": []string{"weekday", "weekend", "holiday"}, "start_minute": 0, "end_minute": 1440, "duration_minutes": duration, "adult_kopecks": adult, "child_kopecks": 0, "hourly_kopecks": hourly, "extra_hourly_kopecks": 0}}}, "resources": resources}
	var dep managedDepartment
	s.call("POST", path+"/departments", s.admin, "", map[string]any{"name": "Original review department", "mode": mode, "active": true, "booking_setup": setup}, 202, &dep)
	return s.waitDepartment(dep.ID)
}

func TestAnalystBackendReview(t *testing.T) {
	s := newSuite(t)
	s.login()
	t.Run("targeted_block_release", func(t *testing.T) {
		s.t = t
		dep := reviewDepartment(s, "session")
		sel := s.find(dep.ID, 2)
		var block reviewBlock
		s.call("POST", "/api/v1/admin/resource-blocks", s.admin, "", map[string]any{"department_id": dep.ID, "resource_ids": sel.ResourceIDs, "starts_at": sel.StartsAt.Add(-time.Hour), "ends_at": sel.EndsAt.Add(time.Hour), "reason": "Review group"}, 201, &block)
		path := "/api/v1/admin/resource-blocks/" + block.ID + "/release"
		body := map[string]any{"version": block.Version, "resource_ids": sel.ResourceIDs[:1], "starts_at": sel.StartsAt, "ends_at": sel.EndsAt}
		s.call("POST", path, "", "", body, 401, nil)
		s.call("POST", "/api/v1/admin/resource-blocks/invalid/release", s.admin, "", body, 400, nil)
		s.call("POST", "/api/v1/admin/resource-blocks/urn:uuid:"+uuid.NewString()+"/release", s.admin, "", body, 404, nil)
		invalid := map[string]any{"version": block.Version, "resource_ids": []string{"unknown"}, "starts_at": sel.StartsAt, "ends_at": sel.EndsAt}
		s.call("POST", path, s.admin, "", invalid, 400, nil)
		invalid["resource_ids"] = sel.ResourceIDs[:1]
		invalid["starts_at"] = block.StartsAt.Add(-time.Minute)
		s.call("POST", path, s.admin, "", invalid, 400, nil)
		invalid["starts_at"] = sel.StartsAt
		invalid["version"] = block.Version + 1
		s.call("POST", path, s.admin, "", invalid, 409, nil)
		var result, replay struct {
			Original  reviewBlock   `json:"original"`
			Remaining []reviewBlock `json:"remaining"`
		}
		key := uuid.NewString()
		s.call("POST", path, s.admin, key, body, 200, &result)
		s.call("POST", path, s.admin, key, body, 200, &replay)
		require.Equal(t, result, replay)
		changedBody := map[string]any{"version": block.Version, "resource_ids": sel.ResourceIDs[1:], "starts_at": sel.StartsAt, "ends_at": sel.EndsAt}
		s.call("POST", path, s.admin, key, changedBody, 409, nil)
		require.False(t, result.Original.Active)
		require.Equal(t, block.Version+1, result.Original.Version)
		require.Len(t, result.Remaining, 3)
		s.call("POST", path, s.admin, "", body, 409, nil)
		var audits int
		require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM audit WHERE entity_id=$1 AND action='resource.block.release'", block.ID).Scan(&audits))
		require.Equal(t, 1, audits)
		// Query the actual database at points on each side of the exact cut.
		for i, id := range sel.ResourceIDs {
			for _, point := range []time.Time{sel.StartsAt.Add(-30 * time.Minute), sel.StartsAt, sel.EndsAt.Add(-time.Microsecond), sel.EndsAt} {
				var n int
				require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM resource_blocks WHERE active AND department_id=$1 AND $2=ANY(resource_ids) AND starts_at<=$3 AND ends_at>$3", dep.ID, id, point).Scan(&n))
				want := 1
				if i == 0 && !point.Before(sel.StartsAt) && point.Before(sel.EndsAt) {
					want = 0
				}
				require.Equal(t, want, n)
			}
		}
		chosen := sel
		chosen.ResourceIDs = sel.ResourceIDs[:1]
		chosen.Guests = []map[string]any{{"category": "adult", "count": 1}}
		c := s.create(chosen)
		s.hold(c)
		s.release(c)
		chosen.ResourceIDs = sel.ResourceIDs[1:]
		other := s.create(chosen)
		s.call("POST", "/api/v1/orders/"+other.Order.ID+"/hold", other.Token, "", map[string]any{}, 409, nil)
		// Two staff requests based on the same block version cannot both take effect.
		remaining := result.Remaining[0]
		raceBody := map[string]any{"version": remaining.Version, "resource_ids": remaining.ResourceIDs, "starts_at": remaining.StartsAt, "ends_at": remaining.EndsAt}
		codes := make([]int, 2)
		errs := make([]error, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				codes[i], _, errs[i] = s.raw("POST", "/api/v1/admin/resource-blocks/"+remaining.ID+"/release", s.admin, uuid.NewString(), raceBody)
			}(i)
		}
		close(start)
		wg.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		require.ElementsMatch(t, []int{200, 409}, codes)
	})
	t.Run("sauna_minutes_snapshot_and_pagination", func(t *testing.T) {
		s.t = t
		dep := reviewDepartment(s, "sauna")
		loc := time.FixedZone("YEKT", 5*3600)
		tomorrow := s.now.In(loc).AddDate(0, 0, 1)
		from := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 12, 30, 0, 0, loc)
		var layout struct {
			Resources []resource `json:"resources"`
		}
		s.call("GET", "/api/v1/departments/"+dep.ID+"/resources", "", "", nil, 200, &layout)
		sel := selection{DepartmentID: dep.ID, ResourceIDs: []string{layout.Resources[0].ID}, StartsAt: from, EndsAt: from.Add(time.Hour), Guests: []map[string]any{{"category": "adult", "count": 1}}}
		var available struct {
			Items []struct {
				StartsAt time.Time `json:"starts_at"`
				EndsAt   time.Time `json:"ends_at"`
			} `json:"items"`
		}
		s.call("GET", "/api/v1/departments/"+dep.ID+"/availability?date="+from.Format("2006-01-02")+"&duration_minutes=60", "", "", nil, 200, &available)
		require.Equal(t, 30, available.Items[0].StartsAt.In(loc).Minute())
		require.Equal(t, "21:30", available.Items[len(available.Items)-1].EndsAt.In(loc).Format("15:04"))
		createBody := map[string]any{"selection": sel, "buyer": map[string]string{"name": "Review", "phone": "+79000000000"}}
		createKey := uuid.NewString()
		// Discard the first successful response, as if the connection was lost.
		s.call("POST", "/api/v1/orders", "", createKey, createBody, 201, nil)
		var c created
		s.call("POST", "/api/v1/orders", "", createKey, createBody, 201, &c)
		var duplicates int
		require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM orders WHERE department_id=$1", dep.ID).Scan(&duplicates))
		require.Equal(t, 1, duplicates)
		h := s.hold(c)
		declineKey := uuid.NewString()
		declineBody := paymentBody(h, "declined")
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, declineKey, declineBody, 202, nil)
		var declined attempt
		declineStatus, declineData, declineErr := s.raw("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, declineKey, declineBody)
		require.NoError(t, declineErr)
		require.Contains(t, []int{200, 202}, declineStatus)
		require.NoError(t, json.Unmarshal(declineData, &declined))
		s.waitAttempt(c, declined, "declined")
		require.Equal(t, "held", s.read(c).Status)
		var paid attempt
		s.call("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, "", paymentBody(h, "success"), 202, &paid)
		s.waitAttempt(c, paid, "accepted")
		o := s.read(c)
		u, err := url.Parse(o.TicketURL)
		require.NoError(t, err)
		var before, after map[string]any
		s.call("GET", u.Path, "", "", nil, 200, &before)
		visit := before["visit"].(map[string]any)
		require.Equal(t, "Original review department", visit["department_name"])
		storedStart, err := time.Parse(time.RFC3339, visit["starts_at"].(string))
		require.NoError(t, err)
		require.True(t, from.Equal(storedStart))
		secondSelection := sel
		secondSelection.StartsAt = from.Add(2 * time.Hour)
		secondSelection.EndsAt = secondSelection.StartsAt.Add(time.Hour)
		second := s.create(secondSelection)
		secondHeld := s.hold(second)
		s.call("POST", "/api/v1/orders/"+second.Order.ID+"/payments", second.Token, "", paymentBody(secondHeld, "success"), 202, &paid)
		s.waitAttempt(second, paid, "accepted")
		// Both rename and archive must leave public ticket and admin order snapshots intact.
		adminPath := "/api/v1/admin/departments/" + dep.ID
		s.call("PATCH", adminPath, s.admin, "", map[string]any{"version": dep.Version, "name": "Renamed review department"}, 202, nil)
		dep = s.waitDepartment(dep.ID)
		s.call("PATCH", adminPath, s.admin, "", map[string]any{"version": dep.Version, "active": false}, 202, nil)
		s.waitDepartment(dep.ID)
		s.call("GET", "/api/v1/departments/"+dep.ID, "", "", nil, 404, nil)
		s.call("GET", u.Path, "", "", nil, 200, &after)
		require.Equal(t, before, after)
		status, pdf, err := s.raw("GET", u.Path+"/pdf", "", "", nil)
		require.NoError(t, err)
		require.Equal(t, 200, status)
		require.True(t, strings.HasPrefix(string(pdf), "%PDF-"))
		var historical map[string]any
		s.call("GET", "/api/v1/admin/orders/"+c.Order.ID, s.admin, "", nil, 200, &historical)
		require.Equal(t, visit, historical["quote"])
		// Filtering must happen before pagination, including for archived departments.
		s.call("POST", "/api/v1/orders", "", "", map[string]any{"selection": sel, "buyer": map[string]string{"name": "Review", "phone": "+79000000000"}}, 409, nil)
		var page struct {
			Items []order `json:"items"`
		}
		query := "?department_id=" + url.QueryEscape(dep.ID) + "&status=confirmed&limit=1"
		s.call("GET", "/api/v1/admin/orders"+query, s.admin, "", nil, 200, &page)
		require.Len(t, page.Items, 1)
		firstID := page.Items[0].ID
		require.Contains(t, []string{c.Order.ID, second.Order.ID}, firstID)
		s.call("GET", "/api/v1/admin/orders"+query+"&offset=1", s.admin, "", nil, 200, &page)
		require.Len(t, page.Items, 1)
		require.NotEqual(t, firstID, page.Items[0].ID)
		require.Contains(t, []string{c.Order.ID, second.Order.ID}, page.Items[0].ID)
		s.call("GET", "/api/v1/admin/orders"+query+"&offset=2", s.admin, "", nil, 200, &page)
		require.Empty(t, page.Items)
	})
}
