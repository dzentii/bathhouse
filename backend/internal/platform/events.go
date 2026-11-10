package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

type Event struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Version     int             `json:"version"`
	AggregateID string          `json:"aggregate_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Data        json.RawMessage `json:"data"`
}
type PaymentResult struct {
	PaymentID string `json:"payment_id"`
	OrderID   string `json:"order_id"`
	Amount    int64  `json:"amount_kopecks"`
	Currency  string `json:"currency"`
	Outcome   string `json:"outcome"`
}
type PaymentProcessed struct {
	PaymentID string `json:"payment_id"`
	OrderID   string `json:"order_id"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

func Emit(ctx context.Context, tx pgx.Tx, typ, aggregate string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	e := Event{uuid.NewString(), typ, 1, aggregate, time.Now().UTC(), b}
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO outbox(id,aggregate_id,payload) VALUES($1,$2,$3)", e.ID, aggregate, payload)
	return err
}
func Inbox(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	res, err := tx.Exec(ctx, "INSERT INTO inbox(id) VALUES($1) ON CONFLICT DO NOTHING", id)
	return res.RowsAffected() == 1, err
}

// RunPublisher provides at-least-once delivery. The row remains pending until Kafka acknowledges it.
func RunPublisher(ctx context.Context, db *pgxpool.Pool, c Config) {
	w := &kafka.Writer{Addr: kafka.TCP(c.KafkaAddr), Topic: c.Topic, Balancer: &kafka.Hash{}, RequiredAcks: kafka.RequireAll, WriteTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, BatchTimeout: 10 * time.Millisecond, MaxAttempts: 3}
	defer func() { _ = w.Close() }()
	timer := time.NewTicker(300 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := publishBatch(ctx, db, w); err != nil && ctx.Err() == nil {
				slog.Warn("outbox delivery deferred", "service", c.Service)
			}
		}
	}
}
func publishBatch(ctx context.Context, db *pgxpool.Pool, w *kafka.Writer) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, "SELECT id::text,aggregate_id,payload,attempts FROM outbox WHERE delivered_at IS NULL AND next_attempt_at<=clock_timestamp() ORDER BY created_at,id LIMIT 10 FOR UPDATE SKIP LOCKED")
	if err != nil {
		return err
	}
	type pending struct {
		id, key  string
		payload  []byte
		attempts int
	}
	items := []pending{}
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.key, &p.payload, &p.attempts); err != nil {
			rows.Close()
			return err
		}
		items = append(items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range items {
		send, cancel := context.WithTimeout(ctx, 6*time.Second)
		e := w.WriteMessages(send, kafka.Message{Key: []byte(p.key), Value: p.payload})
		cancel()
		if e != nil {
			deliveryFailures.Add(1)
			delay := time.Second * time.Duration(1<<min(p.attempts, 6))
			_, err = tx.Exec(ctx, "UPDATE outbox SET attempts=attempts+1,next_attempt_at=clock_timestamp()+$2::interval WHERE id=$1", p.id, delay.String())
		} else {
			_, err = tx.Exec(ctx, "UPDATE outbox SET delivered_at=clock_timestamp(),attempts=attempts+1 WHERE id=$1", p.id)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RunConsumer commits only after a handler transaction or a durable DLQ write succeeds.
func RunConsumer(ctx context.Context, c Config, group string, handle func(context.Context, Event) error) {
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{c.KafkaAddr}, Topic: c.Topic, GroupID: group, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: time.Second, CommitInterval: 0, StartOffset: kafka.FirstOffset})
	defer func() { _ = r.Close() }()
	w := &kafka.Writer{Addr: kafka.TCP(c.KafkaAddr), Topic: c.Topic + ".dlq", RequiredAcks: kafka.RequireAll, WriteTimeout: 5 * time.Second}
	defer func() { _ = w.Close() }()
	for ctx.Err() == nil {
		m, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				pause(ctx, time.Second)
			}
			continue
		}
		var e Event
		err = json.Unmarshal(m.Value, &e)
		if err == nil && (uuid.Validate(e.ID) != nil || e.Version != 1 || e.Type == "" || len(e.Data) == 0) {
			err = errors.New("invalid event envelope")
		}
		if err == nil {
			for attempt := 0; attempt < 5; attempt++ {
				err = handle(ctx, e)
				if err == nil || ctx.Err() != nil {
					break
				}
				pause(ctx, time.Duration(1<<attempt)*200*time.Millisecond)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// Preserve the original event for explicit replay; avoid including untrusted error text.
			eventID := e.ID
			if uuid.Validate(eventID) != nil {
				eventID = "invalid"
			}
			slog.Error("event moved to dead letter queue", "service", c.Service, "event_id", eventID)
			for ctx.Err() == nil {
				err = w.WriteMessages(ctx, kafka.Message{Key: m.Key, Value: m.Value, Headers: []kafka.Header{{Key: "failed_group", Value: []byte(group)}}})
				if err == nil {
					deadLetters.Add(1)
					break
				}
				pause(ctx, time.Second)
			}
		}
		for ctx.Err() == nil {
			if r.CommitMessages(ctx, m) == nil {
				break
			}
			pause(ctx, time.Second)
		}
	}
}
func pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
