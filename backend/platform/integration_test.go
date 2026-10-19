package platform

// These tests need real RabbitMQ and Postgres, so they only run when you point them at the compose stack:
//   docker compose up -d rabbitmq postgres
//   RABBITMQ_URL=amqp://guest:guest@localhost:5672/ DATABASE_URL=postgres://braillebar:braillebar@localhost:5432/braillebar?search_path=document go test ./...
// Without the variables they are skipped.

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func needEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s not set", key)
	}
	return v
}

func TestProcessOnceSkipsDuplicates(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, needEnv(t, "DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	id := newID()
	var runs int
	work := func(pgx.Tx) error { runs++; return nil }

	first, err := ProcessOnce(ctx, pool, id, work)
	if err != nil || !first {
		t.Fatalf("first delivery: processed=%v err=%v", first, err)
	}
	second, err := ProcessOnce(ctx, pool, id, work)
	if err != nil || second {
		t.Fatalf("duplicate delivery: processed=%v err=%v", second, err)
	}
	if runs != 1 {
		t.Fatalf("work ran %d times, want 1", runs)
	}
}

func TestProcessOnceRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, needEnv(t, "DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	id := newID()
	if _, err := ProcessOnce(ctx, pool, id, func(pgx.Tx) error { return errors.New("boom") }); err == nil {
		t.Fatal("expected the error to come back")
	}
	// The failed attempt must not have marked the event as done, so a retry still processes it.
	ok, err := ProcessOnce(ctx, pool, id, func(pgx.Tx) error { return nil })
	if err != nil || !ok {
		t.Fatalf("retry after failure: processed=%v err=%v", ok, err)
	}
}

func TestPublishConsumeAndDeadLetter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, err := Connect(ctx, needEnv(t, "RABBITMQ_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	queue := "test." + newID()
	got := make(chan Envelope, 1)
	var attempts atomic.Int32
	go b.Consume(ctx, queue, []string{DocumentUploadedEvent, PrintJobCreatedEvent}, func(_ context.Context, e Envelope) error {
		if e.Type == PrintJobCreatedEvent {
			attempts.Add(1)
			return errors.New("always fails")
		}
		got <- e
		return nil
	})
	time.Sleep(time.Second) // let the queue and bindings exist before publishing

	good, _ := NewEnvelope(DocumentUploadedEvent, DocumentUploaded{DocumentID: "d", DeviceID: "v", Source: "text"})
	if err := b.Publish(ctx, good); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-got:
		if e.ID != good.ID {
			t.Fatalf("got event %s, want %s", e.ID, good.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event was not delivered")
	}

	bad, _ := NewEnvelope(PrintJobCreatedEvent, PrintJobCreated{JobID: "j", Pages: [][]string{}})
	if err := b.Publish(ctx, bad); err != nil {
		t.Fatal(err)
	}
	// After the retries it must be kept in the dead queue, not lost.
	ch, _ := b.conn.Channel()
	defer ch.Close()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if msg, ok, _ := ch.Get(queue+".dead", true); ok {
			if attempts.Load() != maxAttempts {
				t.Fatalf("handler ran %d times before dead-lettering, want %d", attempts.Load(), maxAttempts)
			}
			_ = msg
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("failed event never reached the dead queue")
}
