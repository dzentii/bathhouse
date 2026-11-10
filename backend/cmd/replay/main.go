package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
)

func main() {
	broker := flag.String("broker", "localhost:19092", "Kafka broker")
	topic := flag.String("topic", "banya.events.v1", "destination topic")
	group := flag.String("group", "", "failed consumer group to replay")
	limit := flag.Int("limit", 1, "maximum events to replay")
	flag.Parse()
	if *group == "" || *limit < 1 {
		log.Fatal("-group and positive -limit required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{*broker}, Topic: *topic + ".dlq", GroupID: "replay-" + *group, StartOffset: kafka.FirstOffset})
	defer func() { _ = r.Close() }()
	w := &kafka.Writer{Addr: kafka.TCP(*broker), Topic: *topic, RequiredAcks: kafka.RequireAll}
	defer func() { _ = w.Close() }()
	for n := 0; n < *limit; {
		m, e := r.FetchMessage(ctx)
		if e != nil {
			log.Fatal("cannot fetch DLQ event")
		}
		match := false
		for _, h := range m.Headers {
			if h.Key == "failed_group" && string(h.Value) == *group {
				match = true
			}
		}
		if match {
			if e = w.WriteMessages(ctx, kafka.Message{Key: m.Key, Value: m.Value}); e != nil {
				log.Fatal("cannot replay event")
			}
			n++
		}
		if e = r.CommitMessages(ctx, m); e != nil {
			log.Fatal("cannot commit DLQ offset")
		}
	}
	log.Print("replay completed")
}
