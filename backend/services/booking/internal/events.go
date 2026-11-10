package booking

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) HandleEvent(ctx context.Context, e platform.Event) error {
	if e.Type == "catalog.department.updated" {
		return s.applyCatalog(ctx, e)
	}
	if e.Type == "department.changed" {
		var v struct {
			DepartmentID string `json:"department_id"`
		}
		if err := json.Unmarshal(e.Data, &v); err != nil {
			return err
		}
		_ = s.Redis.Del(ctx, "booking:config:v4:"+v.DepartmentID).Err()
		return nil
	}
	if e.Type != "payment.result" {
		return nil
	}
	var result platform.PaymentResult
	if err := json.Unmarshal(e.Data, &result); err != nil {
		return err
	}
	if uuid.Validate(result.OrderID) != nil || uuid.Validate(result.PaymentID) != nil || (result.Outcome != "success" && result.Outcome != "declined") {
		return errors.New("invalid payment result")
	}
	o, err := getOrder(ctx, s.DB, result.OrderID, false)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	resultDepartment := o.DepartmentID
	_, err = department(ctx, tx, o.DepartmentID, true)
	if err != nil {
		return err
	}
	if err = expireDepartment(ctx, tx, o.DepartmentID); err != nil {
		return err
	}
	fresh, err := platform.Inbox(ctx, tx, e.ID)
	if err != nil {
		return err
	}
	if !fresh {
		return tx.Commit(ctx)
	}
	var decision platform.PaymentProcessed
	decision.PaymentID = result.PaymentID
	decision.OrderID = result.OrderID
	err = tx.QueryRow(ctx, "SELECT status,reason FROM payment_decisions WHERE payment_id=$1", result.PaymentID).Scan(&decision.Status, &decision.Reason)
	if err == nil {
		if err = platform.Emit(ctx, tx, "booking.payment_processed", o.ID, decision); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	o, err = getOrder(ctx, tx, result.OrderID, true)
	if err != nil {
		return err
	}
	if o.DepartmentID != resultDepartment {
		return errors.New("order department changed; retry")
	}
	now, err := nowDB(ctx, tx)
	if err != nil {
		return err
	}
	decision.Status = "rejected"
	switch {
	case o.Status != "held":
		decision.Reason = "order_" + o.Status
	case o.ExpiresAt == nil || !now.Before(*o.ExpiresAt):
		decision.Reason = "hold_expired"
		if err = expireDepartment(ctx, tx, o.DepartmentID); err != nil {
			return err
		}
	case result.Currency != "RUB" || result.Amount != o.Amount:
		decision.Reason = "amount_mismatch"
	case result.Outcome == "declined":
		decision.Status = "declined"
		decision.Reason = "test_declined"
	default:
		decision.Status = "accepted"
		ticket := capability(o.ID, s.Config.TicketKey, "ticket")
		_, err = tx.Exec(ctx, "UPDATE orders SET version=version+1,status='confirmed',confirmed_at=clock_timestamp(),ticket_hash=$2,ticket_expires_at=ends_at+interval '30 days',access_expires_at=ends_at+interval '30 days',contact_purge_at=ends_at+interval '30 days' WHERE id=$1", o.ID, platform.Hash(ticket))
		if err != nil {
			return err
		}
		if err = emitOrder(ctx, tx, "order.confirmed", o.ID, map[string]string{"order_id": o.ID, "payment_id": result.PaymentID}); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "INSERT INTO payment_decisions(payment_id,order_id,status,reason) VALUES($1,$2,$3,$4)", result.PaymentID, o.ID, decision.Status, decision.Reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO order_history(order_id,action,details) VALUES($1,$2,$3)", o.ID, "payment."+decision.Status, bytesJSON(decision)); err != nil {
		return err
	}
	if err = platform.Emit(ctx, tx, "booking.payment_processed", o.ID, decision); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) RunExpiry(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, err := s.DB.Query(ctx, "SELECT DISTINCT department_id FROM orders WHERE status='held' AND expires_at<=clock_timestamp()")
			if err != nil {
				continue
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				func() {
					tx, e := s.DB.Begin(ctx)
					if e != nil {
						return
					}
					defer func() { _ = tx.Rollback(ctx) }()
					if _, e = department(ctx, tx, id, true); e != nil {
						return
					}
					if e = expireDepartment(ctx, tx, id); e != nil {
						return
					}
					if e = tx.Commit(ctx); e != nil && ctx.Err() == nil {
						slog.Warn("expiration retry scheduled")
					}
				}()
			}
		}
	}
}
