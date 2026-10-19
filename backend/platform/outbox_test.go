package platform

// Needs RABBITMQ_URL and DATABASE_URL, like integration_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// An event enqueued in a committed transaction is published; one from a rolled-back transaction never is.
func TestOutboxPublishesOnlyCommittedEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, needEnv(t, "DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	b, err := Connect(ctx, needEnv(t, "RABBITMQ_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	got := make(chan string, 10)
	go b.Consume(ctx, "test."+newID(), []string{DocumentUploadedEvent}, func(_ context.Context, e Envelope) error {
		got <- e.ID
		return nil
	})
	time.Sleep(time.Second) // let the queue exist before anything is published

	committed, _ := NewEnvelope(DocumentUploadedEvent, DocumentUploaded{DocumentID: "d1", DeviceID: "v", Source: "text"})
	rolledBack, _ := NewEnvelope(DocumentUploadedEvent, DocumentUploaded{DocumentID: "d2", DeviceID: "v", Source: "text"})

	tx, _ := pool.Begin(ctx)
	if err := Enqueue(ctx, tx, committed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	tx, _ = pool.Begin(ctx)
	if err := Enqueue(ctx, tx, rolledBack); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)

	go RunOutbox(ctx, pool, b)

	seen := map[string]bool{}
	timeout := time.After(5 * time.Second)
	for !seen[committed.ID] {
		select {
		case id := <-got:
			seen[id] = true
		case <-timeout:
			t.Fatal("committed event was never published")
		}
	}
	time.Sleep(time.Second) // give a wrongly published rolled-back event time to show up
	for len(got) > 0 {
		seen[<-got] = true
	}
	if seen[rolledBack.ID] {
		t.Fatal("event from a rolled-back transaction was published")
	}

	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_id = $1`, committed.ID).Scan(&left)
	if left != 0 {
		t.Fatal("published event was not removed from the outbox")
	}
}

// When the connection goes, Done must close and RunOutbox must stop, so the service can exit and restart.
func TestBrokerLossStopsOutbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, needEnv(t, "DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	b, err := Connect(ctx, needEnv(t, "RABBITMQ_URL"))
	if err != nil {
		t.Fatal(err)
	}

	stopped := make(chan error, 1)
	go func() { stopped <- RunOutbox(ctx, pool, b) }()
	b.conn.Close() // stands in for RabbitMQ going away

	select {
	case <-b.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done did not close after the connection was lost")
	}
	select {
	case err := <-stopped:
		if err != ErrBrokerLost {
			t.Fatalf("RunOutbox returned %v, want ErrBrokerLost", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunOutbox kept running without a broker")
	}
}
