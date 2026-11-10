package booking

import (
	"context"
	"net/http"

	"banya.local/backend/internal/platform"
	"github.com/jackc/pgx/v5"
)

func emitOrder(ctx context.Context, tx pgx.Tx, typ, id string, data any) error {
	if _, err := tx.Exec(ctx, "INSERT INTO order_history(order_id,action,details) VALUES($1,$2,$3)", id, typ, bytesJSON(data)); err != nil {
		return err
	}
	return platform.Emit(ctx, tx, typ, id, data)
}
func (s *Service) reserveClientQuota(r *http.Request, tx pgx.Tx, resources int) (string, error) {
	key := platform.ClientKey(r, s.Config.InternalKey)
	if _, err := tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "hold-client:"+key); err != nil {
		return "", err
	}
	var holds, used int
	err := tx.QueryRow(r.Context(), `SELECT count(*),COALESCE(sum(jsonb_array_length(selection->'resource_ids')),0) FROM orders WHERE hold_client_key=$1 AND status='held' AND expires_at>clock_timestamp()`, key).Scan(&holds, &used)
	if err != nil {
		return "", err
	}
	if holds >= platform.Positive(s.Config.MaxActiveHoldsPerClient, 5) || used+resources > platform.Positive(s.Config.MaxHeldResourcesPerClient, 100) {
		return "", platform.Err(429, "hold_quota_exceeded", "Достигнут лимит активных удержаний; завершите или освободите текущие")
	}
	return key, nil
}
