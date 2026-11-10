package booking

import (
	"fmt"
	"net/http"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
)

type orderSearch struct {
	Phone        string     `json:"phone" validate:"max=32"`
	OrderID      string     `json:"order_id"`
	BathID       *int       `json:"bath_id" validate:"omitempty,min=1"`
	DepartmentID string     `json:"department_id" validate:"max=80"`
	ResourceID   string     `json:"resource_id" validate:"max=100"`
	Status       string     `json:"status" validate:"omitempty,oneof=draft held confirmed released expired"`
	From         *time.Time `json:"from"`
	Until        *time.Time `json:"until"`
	Limit        int        `json:"limit" validate:"min=0,max=100"`
	Offset       int        `json:"offset" validate:"min=0,max=10000"`
}

func searchFilter(in orderSearch) (string, []any, error) {
	if !platform.SafeText(in.DepartmentID, 0, 80, false) || !platform.SafeText(in.ResourceID, 0, 100, false) {
		return "", nil, platform.Bad("Некорректное отделение или ресурс")
	}
	where := " WHERE true"
	args := []any{}
	add := func(expression string, v any) { args = append(args, v); where += fmt.Sprintf(expression, len(args)) }
	if in.OrderID != "" {
		if uuid.Validate(in.OrderID) != nil {
			return "", nil, platform.Bad("Некорректный order_id")
		}
		add(" AND o.id=$%d", in.OrderID)
	}
	if in.Phone != "" {
		b, err := normalizeBuyer(Buyer{Name: "search", Phone: in.Phone})
		if err != nil {
			return "", nil, err
		}
		add(" AND o.buyer->>'phone'=$%d", b.Phone)
		where += " AND o.contacts_purged_at IS NULL AND o.contact_purge_at>clock_timestamp() AND o.access_expires_at>clock_timestamp()"
	}
	if in.BathID != nil {
		add(" AND d.bath_id=$%d", *in.BathID)
	}
	if in.DepartmentID != "" {
		add(" AND o.department_id=$%d", in.DepartmentID)
	}
	if in.ResourceID != "" {
		add(" AND (o.selection->'resource_ids') ? $%d", in.ResourceID)
	}
	if in.Status != "" {
		add(" AND (CASE WHEN o.status='held' AND o.expires_at<=clock_timestamp() THEN 'expired' ELSE o.status END)=$%d", in.Status)
	}
	if in.From != nil && in.Until != nil && !in.Until.After(*in.From) {
		return "", nil, platform.Bad("until должен быть позже from")
	}
	if in.From != nil {
		add(" AND o.starts_at>=$%d", *in.From)
	}
	if in.Until != nil {
		add(" AND o.starts_at<$%d", *in.Until)
	}
	return where, args, nil
}
func (s *Service) searchOrders(w http.ResponseWriter, r *http.Request) {
	var in orderSearch
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	where, args, err := searchFilter(in)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	args = append(args, in.Limit, in.Offset)
	rows, err := s.DB.Query(r.Context(), "SELECT o.id::text FROM orders o JOIN departments d ON d.id=o.department_id"+where+fmt.Sprintf(" ORDER BY o.created_at DESC,o.id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	items := []Order{}
	for rows.Next() {
		var o Order
		if err = rows.Scan(&o.ID); err != nil {
			rows.Close()
			platform.Fail(w, err)
			return
		}
		items = append(items, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err = s.paymentSummaries(r, items); err != nil {
		platform.Fail(w, err)
		return
	}
	// A read-only search is evaluated afresh. Never persist a multi-order buyer
	// snapshot in an idempotency receipt beyond individual contact deadlines.
	if in.Phone != "" {
		buyer, _ := normalizeBuyer(Buyer{Name: "search", Phone: in.Phone})
		filtered := items[:0]
		for _, o := range items {
			if o.Buyer.Phone == buyer.Phone {
				filtered = append(filtered, o)
			}
		}
		items = filtered
	}
	platform.JSON(w, 200, map[string]any{"items": items, "limit": in.Limit, "offset": in.Offset, "test_mode": true})
}
