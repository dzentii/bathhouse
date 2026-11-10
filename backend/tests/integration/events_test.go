package integration

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"banya.local/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
)

func TestDeadLetterQueue(t *testing.T) {
	s := newSuite(t)
	event := platform.Event{ID: uuid.NewString(), Type: "unsupported.future", Version: 99, AggregateID: uuid.NewString(), OccurredAt: time.Now(), Data: json.RawMessage(`{}`)}
	s.publish(event)
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{"localhost:19092"}, Topic: "banya.events.v1.dlq", GroupID: "integration-dlq-" + uuid.NewString(), StartOffset: kafka.FirstOffset, MinBytes: 1, MaxBytes: 1 << 20})
	defer func() { _ = r.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	seen := map[string]bool{}
	for !seen["booking.v1"] || !seen["payment.v1"] || !seen["catalog.v1"] {
		m, e := r.FetchMessage(ctx)
		require.NoError(t, e)
		var received platform.Event
		if json.Unmarshal(m.Value, &received) == nil && received.ID == event.ID {
			for _, h := range m.Headers {
				if h.Key == "failed_group" {
					seen[string(h.Value)] = true
				}
			}
		}
		require.NoError(t, r.CommitMessages(ctx, m))
	}
	require.True(t, seen["booking.v1"])
	require.True(t, seen["payment.v1"])
	require.True(t, seen["catalog.v1"])
}

func TestPaymentVersusRelease(t *testing.T) {
	s := newSuite(t)
	c := s.create(s.find("b2-male-general", 1))
	h := s.hold(c)
	var wg sync.WaitGroup
	var paymentCode, releaseCode int
	var paymentBodyResponse []byte
	var paymentError, releaseError error
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		paymentCode, paymentBodyResponse, paymentError = s.raw("POST", "/api/v1/orders/"+c.Order.ID+"/payments", c.Token, uuid.NewString(), paymentBody(h, "success"))
	}()
	go func() {
		defer wg.Done()
		<-start
		releaseCode, _, releaseError = s.raw("POST", "/api/v1/orders/"+c.Order.ID+"/release", c.Token, "", map[string]any{})
	}()
	close(start)
	wg.Wait()
	require.NoError(t, paymentError)
	require.NoError(t, releaseError)
	require.Contains(t, []int{202, 409}, paymentCode)
	require.Contains(t, []int{200, 409}, releaseCode)
	o := s.read(c)
	require.Contains(t, []string{"released", "confirmed"}, o.Status)
	if paymentCode == 202 {
		var a attempt
		require.NoError(t, json.Unmarshal(paymentBodyResponse, &a))
		status := "rejected"
		if o.Status == "confirmed" {
			status = "accepted"
		}
		s.waitAttempt(c, a, status)
	}
	var count int
	require.NoError(t, s.db.QueryRow(context.Background(), "SELECT count(*) FROM allocations WHERE active AND order_id=$1", c.Order.ID).Scan(&count))
	if o.Status == "confirmed" {
		require.Equal(t, 1, count)
		require.NotEmpty(t, o.TicketURL)
	} else {
		require.Zero(t, count)
		require.Empty(t, o.TicketURL)
	}
}
