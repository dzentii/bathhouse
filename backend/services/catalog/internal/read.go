package catalog

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type ManagedBath struct {
	Bath
	Active     bool   `json:"active"`
	SyncStatus string `json:"sync_status"`
}
type ManagedDepartment struct {
	Department
	Active     bool                   `json:"active"`
	SyncStatus string                 `json:"sync_status"`
	SyncError  string                 `json:"sync_error"`
	Setup      *platform.CatalogSetup `json:"booking_setup,omitempty"`
}

const bathColumns = "b.id,b.name,b.address,b.description,b.version,b.active,CASE WHEN EXISTS(SELECT 1 FROM departments d WHERE d.bath_id=b.id AND d.sync_status='pending') THEN 'pending' WHEN EXISTS(SELECT 1 FROM departments d WHERE d.bath_id=b.id AND d.sync_status='error') THEN 'error' ELSE 'ready' END"
const deptColumns = "id,bath_id,name,mode,description,version,active,sync_status,sync_error,booking_setup"

func scanBath(row pgx.Row) (ManagedBath, error) {
	var b ManagedBath
	err := row.Scan(&b.ID, &b.Name, &b.Address, &b.Description, &b.Version, &b.Active, &b.SyncStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		err = platform.Err(404, "not_found", "Баня не найдена")
	}
	return b, err
}
func scanDepartment(row pgx.Row) (ManagedDepartment, error) {
	var d ManagedDepartment
	err := row.Scan(&d.ID, &d.BathID, &d.Name, &d.Mode, &d.Description, &d.Version, &d.Active, &d.SyncStatus, &d.SyncError, &d.Setup)
	if errors.Is(err, pgx.ErrNoRows) {
		err = platform.Err(404, "not_found", "Отделение не найдено")
	}
	return d, err
}
func bathID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(chi.URLParam(r, "bathID"))
	if err != nil || id < 1 {
		return 0, platform.Bad("Некорректный идентификатор бани")
	}
	return id, nil
}
func (s *Service) publicBaths(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := platform.Page(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	b, err := s.cachedPage(r, "baths", limit, offset, func() (any, error) {
		rows, err := s.DB.Query(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.active ORDER BY b.id LIMIT $1 OFFSET $2", limit, offset)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []Bath{}
		for rows.Next() {
			b, err := scanBath(rows)
			if err != nil {
				return nil, err
			}
			items = append(items, b.Bath)
		}
		return map[string]any{"items": items, "limit": limit, "offset": offset}, rows.Err()
	})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, b)
}
func (s *Service) publicBath(w http.ResponseWriter, r *http.Request) {
	id, err := bathID(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	b, err := s.cached(r.Context(), "bath-info:"+strconv.Itoa(id), func() (any, error) {
		b, err := scanBath(s.DB.QueryRow(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.id=$1 AND b.active", id))
		return b.Bath, err
	})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, b)
}
func (s *Service) publicDepartments(w http.ResponseWriter, r *http.Request) {
	id, err := bathID(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	limit, offset, err := platform.Page(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	b, err := s.cachedPage(r, "bath:"+strconv.Itoa(id), limit, offset, func() (any, error) {
		if _, err := scanBath(s.DB.QueryRow(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.id=$1 AND b.active", id)); err != nil {
			return nil, err
		}
		rows, err := s.DB.Query(r.Context(), "SELECT id,bath_id,name,mode,description,version FROM departments WHERE bath_id=$1 AND active AND sync_status='ready' ORDER BY mode COLLATE \"C\",id COLLATE \"C\" LIMIT $2 OFFSET $3", id, limit, offset)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []Department{}
		groups := map[string][]Department{"session": {}, "sauna": {}, "info": {}}
		for rows.Next() {
			var d Department
			if err = rows.Scan(&d.ID, &d.BathID, &d.Name, &d.Mode, &d.Description, &d.Version); err != nil {
				return nil, err
			}
			items = append(items, d)
			groups[d.Mode] = append(groups[d.Mode], d)
		}
		return map[string]any{"items": items, "groups": groups, "limit": limit, "offset": offset}, rows.Err()
	})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, b)
}
func (s *Service) publicDepartment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "departmentID")
	b, err := s.cached(r.Context(), "department:"+id, func() (any, error) {
		var d Department
		err := s.DB.QueryRow(r.Context(), "SELECT d.id,d.bath_id,d.name,d.mode,d.description,d.version FROM departments d JOIN baths b ON b.id=d.bath_id WHERE d.id=$1 AND d.active AND b.active AND d.sync_status='ready'", id).Scan(&d.ID, &d.BathID, &d.Name, &d.Mode, &d.Description, &d.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			err = platform.Err(404, "not_found", "Отделение не найдено")
		}
		return d, err
	})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, b)
}
func (s *Service) adminBaths(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := platform.Page(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), "SELECT "+bathColumns+" FROM baths b ORDER BY b.id LIMIT $1 OFFSET $2", limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	items := []ManagedBath{}
	for rows.Next() {
		b, err := scanBath(rows)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		items = append(items, b)
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset})
}
func (s *Service) adminBath(w http.ResponseWriter, r *http.Request) {
	id, err := bathID(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	b, err := scanBath(s.DB.QueryRow(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.id=$1", id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, b)
}
func (s *Service) adminDepartments(w http.ResponseWriter, r *http.Request) {
	id, err := bathID(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if _, err = scanBath(s.DB.QueryRow(r.Context(), "SELECT "+bathColumns+" FROM baths b WHERE b.id=$1", id)); err != nil {
		platform.Fail(w, err)
		return
	}
	limit, offset, err := platform.Page(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), "SELECT "+deptColumns+" FROM departments WHERE bath_id=$1 ORDER BY id LIMIT $2 OFFSET $3", id, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	items := []ManagedDepartment{}
	for rows.Next() {
		d, err := scanDepartment(rows)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		items = append(items, d)
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset})
}
func (s *Service) adminDepartment(w http.ResponseWriter, r *http.Request) {
	d, err := scanDepartment(s.DB.QueryRow(r.Context(), "SELECT "+deptColumns+" FROM departments WHERE id=$1", chi.URLParam(r, "departmentID")))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, d)
}

// Cache only the default page; arbitrary offsets cannot grow the Redis key space.
func (s *Service) cachedPage(r *http.Request, key string, limit, offset int, load func() (any, error)) (json.RawMessage, error) {
	if limit == 50 && offset == 0 {
		return s.cached(r.Context(), key, load)
	}
	value, err := load()
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
