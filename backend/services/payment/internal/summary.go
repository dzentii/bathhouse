package payment

import (
	"net/http"

	"banya.local/backend/internal/platform"
)

// summaries is an internal read operation. Booking calls it only after admin authorization.
func (s *Service) summaries(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OrderIDs []string `json:"order_ids" validate:"required,min=1,max=100,dive,uuid"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT DISTINCT ON(order_id) `+columns+` FROM attempts WHERE order_id=ANY($1::uuid[]) ORDER BY order_id,created_at DESC,id DESC`, in.OrderIDs)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	items := map[string]Attempt{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		items[a.OrderID] = a
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items})
}
