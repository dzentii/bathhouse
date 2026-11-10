package payment

import (
	"context"
	"net/http"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Service) attempts(ctx context.Context, id string, limit, offset int) ([]Attempt, error) {
	rows, err := s.DB.Query(ctx, "SELECT "+columns+" FROM attempts WHERE order_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3", id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Attempt{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}
func (s *Service) listAttempts(w http.ResponseWriter, r *http.Request, admin bool) {
	id := chi.URLParam(r, "orderID")
	if admin {
		if uuid.Validate(id) != nil {
			platform.Fail(w, platform.Bad("Некорректный заказ"))
			return
		}
	} else {
		order, err := s.order(r)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		id = order.ID
	}
	limit, offset, err := platform.Page(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	items, err := s.attempts(r.Context(), id, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset, "test_mode": true})
}
func (s *Service) internalHistory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OrderID  string    `json:"order_id" validate:"required,uuid"`
		Limit    int       `json:"limit" validate:"required,min=1,max=100"`
		Before   time.Time `json:"before" validate:"required"`
		BeforeID string    `json:"before_id" validate:"required,max=128"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), "SELECT "+columns+" FROM attempts WHERE order_id=$1 AND (created_at,('payment:'||id::text) COLLATE \"C\")<($2,$3) ORDER BY created_at DESC,id DESC LIMIT $4", in.OrderID, in.Before, in.BeforeID, in.Limit)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	items := []Attempt{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		items = append(items, a)
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"items": items})
}
