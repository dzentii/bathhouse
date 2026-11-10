package booking

import (
	"net/http"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type occupancyItem struct {
	ID             string     `json:"id"`
	ResourceID     string     `json:"resource_id"`
	ResourceLabel  string     `json:"resource_label"`
	ResourceActive bool       `json:"resource_active"`
	State          string     `json:"state"`
	StartsAt       time.Time  `json:"starts_at"`
	EndsAt         time.Time  `json:"ends_at"`
	VisitEndsAt    *time.Time `json:"visit_ends_at,omitempty"`
	OrderID        string     `json:"order_id,omitempty"`
	BlockID        string     `json:"block_id,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Reason         string     `json:"reason,omitempty"`
}

func occupancyWindow(r *http.Request) (time.Time, time.Time, error) {
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		return from, time.Time{}, platform.Bad("from должен иметь формат RFC3339")
	}
	until, err := time.Parse(time.RFC3339, r.URL.Query().Get("until"))
	if err != nil || !until.After(from) || until.Sub(from) > 31*24*time.Hour {
		return from, until, platform.Bad("from..until: положительный интервал не более 31 дня")
	}
	if !platform.SafeText(r.URL.Query().Get("resource_id"), 0, 100, false) {
		return from, until, platform.Bad("Некорректный resource_id")
	}
	return from, until, nil
}
func (s *Service) occupancy(w http.ResponseWriter, r *http.Request) {
	from, until, err := occupancyWindow(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	limit, offset, err := platform.Page(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	d, err := department(r.Context(), tx, chi.URLParam(r, "departmentID"), false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	now := d.ServerTime
	rows, err := tx.Query(r.Context(), `WITH busy AS (
 SELECT 'allocation:'||a.id::text AS id,r.id AS resource_id,r.label,r.active,CASE WHEN o.status='confirmed' THEN 'booked' ELSE 'held' END AS state,
 lower(a.span) AS starts_at,upper(a.span) AS ends_at,o.ends_at AS visit_ends_at,o.id::text AS order_id,'' AS block_id,
 CASE WHEN o.status='held' THEN o.expires_at END AS expires_at,'' AS reason
 FROM resources r JOIN allocations a ON a.resource_id=r.id JOIN orders o ON o.id=a.order_id
 WHERE r.department_id=$1 AND ($4='' OR r.id=$4) AND a.active AND a.span && tstzrange($2,$3,'[)')
 AND (o.status='confirmed' OR o.status='held' AND o.expires_at>$5)
 UNION ALL
 SELECT 'block:'||b.id::text||':'||r.id,r.id,r.label,r.active,'blocked',b.starts_at,b.ends_at,NULL,'' AS order_id,b.id::text,NULL,b.reason
 FROM resource_blocks b JOIN resources r ON r.department_id=b.department_id AND r.id=ANY(b.resource_ids)
 WHERE b.department_id=$1 AND b.active AND ($4='' OR r.id=$4) AND b.starts_at<$3 AND b.ends_at>$2)
 SELECT id,resource_id,label,active,state,starts_at,ends_at,visit_ends_at,order_id,block_id,expires_at,reason FROM busy
 ORDER BY starts_at,id COLLATE "C" LIMIT $6 OFFSET $7`, d.ID, from, until, r.URL.Query().Get("resource_id"), now, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	items := []occupancyItem{}
	for rows.Next() {
		var v occupancyItem
		if err = rows.Scan(&v.ID, &v.ResourceID, &v.ResourceLabel, &v.ResourceActive, &v.State, &v.StartsAt, &v.EndsAt, &v.VisitEndsAt, &v.OrderID, &v.BlockID, &v.ExpiresAt, &v.Reason); err != nil {
			rows.Close()
			platform.Fail(w, err)
			return
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"department_id": d.ID, "from": from, "until": until, "server_time": now, "limit": limit, "offset": offset, "items": items})
}
