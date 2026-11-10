package catalog

import (
	"net/http"
	"strconv"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func (s *Service) internalHistory(w http.ResponseWriter, r *http.Request) {
	window, err := platform.HistoryQuery(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	id := chi.URLParam(r, "departmentID")
	d, err := scanDepartment(s.DB.QueryRow(r.Context(), "SELECT "+deptColumns+" FROM departments WHERE id=$1", id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,action,entity_id,admin_id,before_value,after_value,created_at FROM (
 SELECT 'catalog:'||id::text AS id,action,entity_id,admin_id::text,before_value,after_value,created_at FROM catalog_changes
 WHERE (entity_type='department' AND entity_id=$1) OR (entity_type='bath' AND entity_id=$2)
 UNION ALL SELECT 'catalog-info:'||id::text,'description.update',department_id,admin_id::text,before_value,after_value,created_at FROM catalog_audit WHERE department_id=$1
 ) history WHERE (created_at,id COLLATE "C")<($3,$4) ORDER BY created_at DESC,id COLLATE "C" DESC LIMIT $5`, id, strconv.Itoa(d.BathID), window.Before, window.BeforeID, window.Limit)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	items := []platform.HistoryEntry{}
	for rows.Next() {
		var e platform.HistoryEntry
		e.Source = "catalog"
		if err = rows.Scan(&e.ID, &e.Action, &e.EntityID, &e.ActorID, &e.Before, &e.After, &e.CreatedAt); err != nil {
			platform.Fail(w, err)
			return
		}
		items = append(items, e)
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items})
}
