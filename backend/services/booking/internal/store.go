package booking

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	DB      *pgxpool.Pool
	Redis   *redis.Client
	Config  platform.Config
	pdfOnce sync.Once
	pdfs    *pdfCache
}
type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func department(ctx context.Context, db queryer, id string, lock bool) (Department, error) {
	var d Department
	sql := "SELECT id,bath_id,bath_name,bath_address,name,mode,base_guests,max_guests,schedule,tariffs,schedule_version,tariff_version,min_duration_minutes,duration_step_minutes,start_step_minutes,buffer_minutes,archived,catalog_version,policy_version,scheme_version,online_sales_enabled,sales_paused_until,sales_pause_reason,clock_timestamp() FROM departments WHERE id=$1"
	if lock {
		sql += " FOR UPDATE"
	}
	err := db.QueryRow(ctx, sql, id).Scan(&d.ID, &d.BathID, &d.BathName, &d.BathAddress, &d.Name, &d.Mode, &d.BaseGuests, &d.MaxGuests, &d.Schedule, &d.Tariffs, &d.ScheduleVersion, &d.TariffVersion, &d.MinDurationMinutes, &d.DurationStepMinutes, &d.StartStepMinutes, &d.BufferMinutes, &d.Archived, &d.CatalogVersion, &d.PolicyVersion, &d.SchemeVersion, &d.OnlineSalesEnabled, &d.SalesPausedUntil, &d.SalesPauseReason, &d.ServerTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, platform.Err(404, "not_found", "Отделение не оформляется онлайн")
	}
	return d, err
}

const orderColumns = "id::text,department_id,status,selection,buyer,quote,amount_kopecks,created_at,held_at,expires_at,confirmed_at,ticket_expires_at,access_hash,request_hash,access_expires_at,contacts_purged_at,version,clock_timestamp()"

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.DepartmentID, &o.Status, &o.Selection, &o.Buyer, &o.Quote, &o.Amount, &o.CreatedAt, &o.HeldAt, &o.ExpiresAt, &o.ConfirmedAt, &o.TicketExpiresAt, &o.AccessHash, &o.RequestHash, &o.AccessExpiresAt, &o.ContactsPurgedAt, &o.Version, &o.ServerTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, platform.Err(404, "not_found", "Заказ не найден")
	}
	if err == nil && !o.ServerTime.Before(o.AccessExpiresAt) {
		o.Buyer = Buyer{}
	}
	return o, err
}
func getOrder(ctx context.Context, db queryer, id string, lock bool) (Order, error) {
	if uuid.Validate(id) != nil {
		return Order{}, platform.Err(404, "not_found", "Заказ не найден")
	}
	q := "SELECT " + orderColumns + " FROM orders WHERE id=$1"
	if lock {
		q += " FOR UPDATE"
	}
	return scanOrder(db.QueryRow(ctx, q, id))
}
func (s *Service) decorate(o *Order) {
	if o.Status == "confirmed" && o.TicketExpiresAt != nil && o.ServerTime.Before(*o.TicketExpiresAt) {
		o.TicketURL = s.Config.PublicURL + "/api/v1/tickets/" + capability(o.ID, s.Config.TicketKey, "ticket")
	}
}
func authorize(o Order, token string) error {
	if !o.ServerTime.Before(o.AccessExpiresAt) || len(token) != 64 || !platform.Equal(o.AccessHash, platform.Hash(token)) {
		return platform.Err(404, "not_found", "Заказ не найден")
	}
	return nil
}
func normalizeBuyer(b Buyer) (Buyer, error) {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" || len([]rune(b.Name)) > 100 {
		return b, platform.Bad("Имя обязательно, не более 100 символов")
	}
	for _, c := range b.Name {
		if unicode.IsControl(c) {
			return b, platform.Bad("Недопустимое имя")
		}
	}
	var digits strings.Builder
	for i, c := range strings.TrimSpace(b.Phone) {
		switch {
		case c >= '0' && c <= '9':
			digits.WriteRune(c)
		case c == '+' && i == 0:
		case c == ' ' || c == '-' || c == '(' || c == ')':
		default:
			return b, platform.Bad("Некорректный телефон")
		}
	}
	n := digits.String()
	if len(n) == 10 {
		n = "7" + n
	}
	if len(n) == 11 && n[0] == '8' {
		n = "7" + n[1:]
	}
	if len(n) < 10 || len(n) > 15 || n[0] == '0' {
		return b, platform.Bad("Телефон должен содержать 10–15 цифр")
	}
	b.Phone = "+" + n
	return b, nil
}
func idempotency(r *http.Request) (string, error) {
	k, e := platform.IdempotencyKey(r)
	if e != nil {
		return "", e
	}
	return platform.Hash(k), nil
}
func nowDB(ctx context.Context, db queryer) (time.Time, error) {
	var t time.Time
	e := db.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&t)
	return t, e
}

func expireDepartment(ctx context.Context, tx pgx.Tx, id string) error {
	rows, e := tx.Query(ctx, "UPDATE orders SET version=version+1,status='expired' WHERE department_id=$1 AND status='held' AND expires_at<=clock_timestamp() RETURNING id::text", id)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var x string
		if e = rows.Scan(&x); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, orderID := range ids {
		if _, e = tx.Exec(ctx, "UPDATE allocations SET active=false WHERE order_id=$1", orderID); e != nil {
			return e
		}
		if e = emitOrder(ctx, tx, "order.expired", orderID, map[string]string{"order_id": orderID}); e != nil {
			return e
		}
	}
	return nil
}

// All mutations use the department lock before an order lock, including administrative changes.
func (s *Service) lockedOrder(ctx context.Context, id, token string, admin bool) (pgx.Tx, Department, Order, error) {
	o, e := getOrder(ctx, s.DB, id, false)
	if e != nil {
		return nil, Department{}, o, e
	}
	if !admin {
		if e = authorize(o, token); e != nil {
			return nil, Department{}, o, e
		}
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return nil, Department{}, o, e
	}
	fail := func(err error) (pgx.Tx, Department, Order, error) {
		_ = tx.Rollback(ctx)
		return nil, Department{}, o, err
	}
	d, e := department(ctx, tx, o.DepartmentID, true)
	if e != nil {
		return fail(e)
	}
	if e = expireDepartment(ctx, tx, d.ID); e != nil {
		return fail(e)
	}
	o, e = getOrder(ctx, tx, id, true)
	if e != nil {
		return fail(e)
	}
	if o.DepartmentID != d.ID {
		return fail(platform.Conflict("department_changed", "Отделение заказа изменилось; повторите запрос"))
	}
	if !admin {
		if e = authorize(o, token); e != nil {
			return fail(e)
		}
	}
	return tx, d, o, nil
}
func resources(ctx context.Context, db queryer, id string) ([]Resource, error) {
	rows, e := db.Query(ctx, "SELECT id,label,x,y FROM resources WHERE department_id=$1 AND active ORDER BY length(label),label", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	a := []Resource{}
	for rows.Next() {
		var r Resource
		if e = rows.Scan(&r.ID, &r.Label, &r.X, &r.Y); e != nil {
			return nil, e
		}
		a = append(a, r)
	}
	return a, rows.Err()
}
func selectedResources(ctx context.Context, db queryer, d Department, sel Selection) ([]Resource, error) {
	all, e := resources(ctx, db, d.ID)
	if e != nil {
		return nil, e
	}
	byID := map[string]Resource{}
	for _, r := range all {
		byID[r.ID] = r
	}
	out := make([]Resource, 0, len(sel.ResourceIDs))
	for _, id := range sel.ResourceIDs {
		r, ok := byID[id]
		if !ok {
			return nil, platform.Bad("Ресурс не принадлежит выбранному отделению")
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func buildQuote(ctx context.Context, db queryer, d Department, sel Selection, now time.Time) (Quote, error) {
	if e := ValidateSelection(d, sel, now, true); e != nil {
		return Quote{}, e
	}
	a, e := selectedResources(ctx, db, d, sel)
	if e != nil {
		return Quote{}, e
	}
	return Calculate(d, sel, a)
}
func spanEnd(d Department, s Selection) time.Time {
	if d.Mode == "sauna" {
		return s.EndsAt.Add(time.Duration(d.BufferMinutes) * time.Minute)
	}
	return s.EndsAt
}
func availability(ctx context.Context, db queryer, d Department, start, end time.Time) ([]Resource, error) {
	rows, e := db.Query(ctx, `SELECT r.id,r.label,r.x,r.y,CASE
 WHEN EXISTS(SELECT 1 FROM resource_blocks b WHERE b.department_id=r.department_id AND b.active AND r.id=ANY(b.resource_ids) AND tstzrange(b.starts_at,b.ends_at,'[)') && tstzrange($2,$3,'[)')) THEN 'blocked'
 WHEN EXISTS(SELECT 1 FROM allocations a JOIN orders o ON o.id=a.order_id WHERE a.resource_id=r.id AND a.active AND a.span && tstzrange($2,$3,'[)') AND o.status='confirmed') THEN 'booked'
 WHEN EXISTS(SELECT 1 FROM allocations a JOIN orders o ON o.id=a.order_id WHERE a.resource_id=r.id AND a.active AND a.span && tstzrange($2,$3,'[)') AND o.status='held' AND o.expires_at>clock_timestamp()) THEN 'held'
 ELSE 'free' END FROM resources r WHERE r.department_id=$1 AND r.active ORDER BY length(r.label),r.label`, d.ID, start, end)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Resource{}
	for rows.Next() {
		var r Resource
		if e = rows.Scan(&r.ID, &r.Label, &r.X, &r.Y, &r.State); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
