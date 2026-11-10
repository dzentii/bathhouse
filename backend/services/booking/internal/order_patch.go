package booking

import (
	"net/http"
	"sort"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func (s *Service) patchOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version   int64      `json:"version" validate:"required,min=1"`
		Selection *Selection `json:"selection"`
		Buyer     *Buyer     `json:"buyer"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if in.Selection == nil && in.Buyer == nil {
		platform.Fail(w, platform.Bad("Не заданы изменения черновика"))
		return
	}
	id := chi.URLParam(r, "orderID")
	token := platform.Bearer(r)
	old, err := getOrder(r.Context(), s.DB, id, false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = authorize(old, token); err != nil {
		platform.Fail(w, err)
		return
	}
	deps := []string{old.DepartmentID}
	if in.Selection != nil && in.Selection.DepartmentID != old.DepartmentID {
		deps = append(deps, in.Selection.DepartmentID)
	}
	sort.Strings(deps)
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	details := map[string]Department{}
	for _, dep := range deps {
		d, err := department(r.Context(), tx, dep, true)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		if err = expireDepartment(r.Context(), tx, dep); err != nil {
			platform.Fail(w, err)
			return
		}
		details[dep] = d
	}
	o, err := getOrder(r.Context(), tx, id, true)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = authorize(o, token); err != nil {
		platform.Fail(w, err)
		return
	}
	op, err := platform.LoadOperation(r, tx, "order:"+id+":"+platform.Hash(token), s.Config.InternalKey)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if op.Replay(w, r) {
		return
	}
	if o.DepartmentID != old.DepartmentID || o.Version != in.Version {
		platform.Fail(w, platform.Conflict("version_conflict", "Черновик уже изменён; загрузите актуальное состояние"))
		return
	}
	if o.Status != "draft" {
		platform.Fail(w, platform.Conflict("order_inactive", "Редактируется только черновик до удержания"))
		return
	}
	if in.Selection != nil {
		o.Selection = *in.Selection
		o.DepartmentID = in.Selection.DepartmentID
	}
	if in.Buyer != nil {
		o.Buyer, err = normalizeBuyer(*in.Buyer)
		if err != nil {
			platform.Fail(w, err)
			return
		}
	}
	o.Selection.StartsAt = o.Selection.StartsAt.UTC()
	o.Selection.EndsAt = o.Selection.EndsAt.UTC()
	now, err := nowDB(r.Context(), tx)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	quote, err := buildQuote(r.Context(), tx, details[o.DepartmentID], o.Selection, now)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), "UPDATE orders SET department_id=$2,selection=$3,buyer=$4,quote=$5,amount_kopecks=$6,starts_at=$7,ends_at=$8,version=version+1 WHERE id=$1", id, o.DepartmentID, bytesJSON(o.Selection), bytesJSON(o.Buyer), bytesJSON(quote), quote.Amount, o.Selection.StartsAt, o.Selection.EndsAt)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	changes := map[string]bool{"selection_changed": in.Selection != nil, "buyer_changed": in.Buyer != nil}
	if _, err = tx.Exec(r.Context(), "INSERT INTO order_history(order_id,action,details) VALUES($1,'order.updated',$2)", id, bytesJSON(changes)); err != nil {
		platform.Fail(w, err)
		return
	}
	if err = platform.Emit(r.Context(), tx, "order.updated", id, map[string]any{"order_id": id, "version": o.Version + 1}); err != nil {
		platform.Fail(w, err)
		return
	}
	o, err = getOrder(r.Context(), tx, id, false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	op.BindOrder(o.ID, o.AccessExpiresAt)
	op.Commit(w, r, 200, o)
}
