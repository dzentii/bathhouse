package identity

import (
	"context"
	"encoding/json"
	"net/http"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) employeeCard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "employeeID")
	if uuid.Validate(id) != nil {
		platform.Fail(w, platform.Bad("Некорректный employeeID"))
		return
	}
	e, err := employee(s.DB.QueryRow(r.Context(), "SELECT "+employeeColumns+" FROM admins WHERE id=$1", id))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, e)
}
func auditState(e Employee) map[string]any {
	return map[string]any{"role": e.Role, "active": e.Active, "version": e.Version}
}
func employeeAuditDetails(ctx context.Context, tx pgx.Tx, actor, id, action string, before, after any) error {
	var b, a []byte
	var err error
	if before != nil {
		b, err = json.Marshal(before)
		if err != nil {
			return err
		}
	}
	if after != nil {
		a, err = json.Marshal(after)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, "INSERT INTO employee_audit(actor_id,employee_id,action,before_value,after_value) VALUES($1,$2,$3,$4,$5)", actor, id, action, b, a)
	return err
}
func auditEmployeePatch(ctx context.Context, tx pgx.Tx, actor string, before, after Employee, passwordReset bool) error {
	actions := []string{}
	if before.Role != after.Role {
		actions = append(actions, "employee.role_changed")
	}
	if before.Active != after.Active {
		if after.Active {
			actions = append(actions, "employee.activated")
		} else {
			actions = append(actions, "employee.blocked")
		}
	}
	if passwordReset {
		actions = append(actions, "employee.password_reset")
	}
	if before.DisplayName != after.DisplayName {
		actions = append(actions, "employee.profile_changed")
	}
	if len(actions) == 0 {
		actions = append(actions, "employee.update")
	}
	for _, action := range actions {
		b, a := auditState(before), auditState(after)
		if action == "employee.password_reset" {
			a["password_changed"] = true
		}
		if err := employeeAuditDetails(ctx, tx, actor, after.ID, action, b, a); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) securityEvents(w http.ResponseWriter, r *http.Request) {
	window, err := platform.HistoryQuery(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	employeeID, actorID, action := r.URL.Query().Get("employee_id"), r.URL.Query().Get("actor_id"), r.URL.Query().Get("action")
	for _, id := range []string{employeeID, actorID} {
		if id != "" && uuid.Validate(id) != nil {
			platform.Fail(w, platform.Bad("Некорректный идентификатор сотрудника"))
			return
		}
	}
	if !platform.SafeText(action, 0, 80, false) {
		platform.Fail(w, platform.Bad("Некорректное действие"))
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,employee_id,actor_id,action,before_value,after_value,created_at FROM (
 SELECT 'identity:'||id::text AS id,employee_id::text,actor_id::text,action,before_value,after_value,created_at FROM employee_audit
 WHERE ($1::uuid IS NULL OR employee_id=$1) AND ($2::uuid IS NULL OR actor_id=$2) AND ($3='' OR action=$3)
 ) events WHERE (created_at,id COLLATE "C")<($4,$5) ORDER BY created_at DESC,id COLLATE "C" DESC LIMIT $6`, nullableUUID(employeeID), nullableUUID(actorID), action, window.Before, window.BeforeID, window.Limit)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	items := []platform.HistoryEntry{}
	for rows.Next() {
		var e platform.HistoryEntry
		e.Source = "identity"
		if err = rows.Scan(&e.ID, &e.EntityID, &e.ActorID, &e.Action, &e.Before, &e.After, &e.CreatedAt); err != nil {
			platform.Fail(w, err)
			return
		}
		items = append(items, e)
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.HistoryResponse(w, items, window.Limit)
}
func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}
