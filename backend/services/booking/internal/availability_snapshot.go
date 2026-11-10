package booking

import (
	"context"
	"sort"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/jackc/pgx/v5/pgtype"
)

type availabilitySnapshot struct {
	resources []Resource
	busy      map[string]map[string]pgtype.Multirange[pgtype.Range[time.Time]]
}

func (s *Service) loadAvailability(ctx context.Context, d Department, from, to, now time.Time) (availabilitySnapshot, error) {
	snap := availabilitySnapshot{busy: map[string]map[string]pgtype.Multirange[pgtype.Range[time.Time]]{}}
	var err error
	snap.resources, err = resources(ctx, s.DB, d.ID)
	if err != nil {
		return snap, err
	}
	rows, err := s.DB.Query(ctx, `WITH busy AS MATERIALIZED (
 SELECT a.resource_id,a.span,CASE WHEN o.status='confirmed' THEN 'booked' ELSE 'held' END AS state
 FROM allocations a JOIN orders o ON o.id=a.order_id JOIN resources r ON r.id=a.resource_id
 WHERE r.department_id=$1 AND r.active AND a.active AND a.span && tstzrange($2,$3,'[)') AND (o.status='confirmed' OR o.status='held' AND o.expires_at>$4)
 UNION ALL SELECT r.id,tstzrange(b.starts_at,b.ends_at,'[)'),'blocked'
 FROM resource_blocks b JOIN resources r ON r.department_id=b.department_id AND r.id=ANY(b.resource_ids) AND r.active
	 WHERE b.department_id=$1 AND b.active AND b.starts_at<$3 AND b.ends_at>$2 LIMIT 100001)
	 SELECT resource_id,state,range_agg(span),(SELECT count(*) FROM busy) FROM busy GROUP BY resource_id,state`, d.ID, from, to, now)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, state string
		var spans pgtype.Multirange[pgtype.Range[time.Time]]
		var sourceCount int64
		if err = rows.Scan(&id, &state, &spans, &sourceCount); err != nil {
			return snap, err
		}
		count += len(spans)
		if sourceCount > 100000 || count > 100000 {
			return snap, platform.Err(503, "availability_limit", "Диапазон слишком велик для безопасного расчёта")
		}
		if snap.busy[id] == nil {
			snap.busy[id] = map[string]pgtype.Multirange[pgtype.Range[time.Time]]{}
		}
		snap.busy[id][state] = spans
	}
	return snap, rows.Err()
}
func (a availabilitySnapshot) state(id string, start, end time.Time) string {
	for _, state := range []string{"blocked", "booked", "held"} {
		spans := a.busy[id][state]
		index := sort.Search(len(spans), func(i int) bool { return spans[i].Upper.After(start) })
		if index < len(spans) && spans[index].Lower.Before(end) {
			return state
		}
	}
	return "free"
}
func (a availabilitySnapshot) states(start, end time.Time) []Resource {
	out := make([]Resource, len(a.resources))
	copy(out, a.resources)
	for i := range out {
		out[i].State = a.state(out[i].ID, start, end)
	}
	return out
}

// Iterate candidate intervals without building quotes, price hashes or detailed resource arrays.
func visitCandidates(ctx context.Context, d Department, date time.Time, duration int, now time.Time, resource string, visit func(Selection, string)) error {
	if d.Archived {
		return nil
	}
	for _, slot := range slotsFor(d.Schedule, date) {
		start, _ := minute(slot.Start)
		end, _ := minute(slot.End)
		for m := start; m < end; {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := end
			if d.Mode == "sauna" {
				n = m + duration
			}
			if n > end {
				break
			}
			sel := Selection{DepartmentID: d.ID, ResourceIDs: []string{resource}, StartsAt: date.Add(time.Duration(m) * time.Minute), EndsAt: date.Add(time.Duration(n) * time.Minute), Guests: []Guest{{Age: 18, Count: 1}}}
			if ValidateSelection(d, sel, now, true) == nil && sellable(d, sel) {
				visit(sel, slot.Gender)
			}
			if d.Mode == "session" {
				break
			}
			m += d.StartStepMinutes
		}
	}
	return nil
}
func sellable(d Department, s Selection) bool {
	if d.Mode == "session" {
		r, err := tariffAt(d, s.StartsAt, int(s.EndsAt.Sub(s.StartsAt)/time.Minute))
		return err == nil && r.Adult > 0
	}
	numerator := int64(0)
	for t := s.StartsAt; t.Before(s.EndsAt); {
		r, err := tariffAt(d, t, 0)
		if err != nil {
			return false
		}
		end := dayStart(t).Add(time.Duration(r.EndMinute) * time.Minute)
		if end.After(s.EndsAt) {
			end = s.EndsAt
		}
		if !end.After(t) {
			return false
		}
		numerator += r.Hourly * int64(end.Sub(t)/time.Minute)
		t = end
	}
	return (numerator+30)/60 > 0
}
