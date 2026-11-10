package booking

import (
	"context"
	"time"

	"banya.local/backend/internal/platform"
)

// PurgeContacts removes every linked encrypted copy as well as the canonical contact.
func (s *Service) PurgeContacts(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for {
		tag, err := s.DB.Exec(ctx, `WITH expired AS (SELECT id FROM orders WHERE contacts_purged_at IS NULL AND contact_purge_at<=clock_timestamp() ORDER BY contact_purge_at LIMIT 500 FOR UPDATE SKIP LOCKED),
 purged AS (UPDATE orders SET status=CASE WHEN status='draft' THEN 'expired' ELSE status END,buyer='{}'::jsonb,access_hash='',request_hash='',contacts_purged_at=clock_timestamp() WHERE id IN(SELECT id FROM expired) RETURNING id),
 removed AS (DELETE FROM api_requests WHERE order_id IN(SELECT id FROM purged) RETURNING 1)
 SELECT id FROM purged`)
		if err != nil {
			return err
		}
		if tag.RowsAffected() < 500 {
			break
		}
	}
	// Include receipts of orders already purged before an interrupted cleanup or upgrade.
	for {
		tag, err := s.DB.Exec(ctx, `DELETE FROM api_requests WHERE (scope,key_hash) IN(SELECT r.scope,r.key_hash FROM api_requests r JOIN orders o ON o.id=r.order_id WHERE o.contact_purge_at<=clock_timestamp() LIMIT 500 FOR UPDATE OF r SKIP LOCKED)`)
		if err != nil {
			return err
		}
		if tag.RowsAffected() < 500 {
			break
		}
	}
	return platform.CleanupDelivery(ctx, s.DB, s.Config)
}
