package platform

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type executor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Only transport receipts are pruned. Business decisions and anonymized order/audit
// history remain durable, making late replays safe beyond the inbox window.
func CleanupDelivery(ctx context.Context, db executor, c Config) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, item := range []struct {
		sql  string
		days int
	}{
		{`DELETE FROM outbox WHERE id IN(SELECT id FROM outbox WHERE delivered_at<clock_timestamp()-$1::interval ORDER BY delivered_at LIMIT 500 FOR UPDATE SKIP LOCKED)`, Positive(c.OutboxRetentionDays, 30)},
		{`DELETE FROM inbox WHERE id IN(SELECT id FROM inbox WHERE processed_at<clock_timestamp()-$1::interval ORDER BY processed_at LIMIT 500 FOR UPDATE SKIP LOCKED)`, Positive(c.InboxRetentionDays, 90)},
	} {
		for {
			tag, err := db.Exec(ctx, item.sql, (time.Duration(item.days) * 24 * time.Hour).String())
			if err != nil {
				return err
			}
			if tag.RowsAffected() < 500 {
				break
			}
		}
	}
	return nil
}
func RunDeliveryCleanup(ctx context.Context, db executor, c Config) {
	clean := func() {
		if err := CleanupDelivery(ctx, db, c); err != nil && ctx.Err() == nil {
			slog.Warn("delivery cleanup deferred")
		}
	}
	clean()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clean()
		}
	}
}
