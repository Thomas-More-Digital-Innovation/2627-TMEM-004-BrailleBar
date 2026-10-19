package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Transactional outbox: an event is written to this table in the same transaction as the change that caused it,
// so a crash can never commit the change and lose the event (or the other way round). RunOutbox publishes it after.
const outboxTable = `CREATE TABLE IF NOT EXISTS outbox (
	seq        bigserial PRIMARY KEY,
	event_id   text NOT NULL UNIQUE,
	event_type text NOT NULL,
	body       bytea NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
)`

const (
	outboxBatch = 100
	outboxPoll  = 500 * time.Millisecond
)

var ErrBrokerLost = errors.New("broker connection lost")

// Enqueue stores e inside tx. It is published only if tx commits.
func Enqueue(ctx context.Context, tx pgx.Tx, e Envelope) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox (event_id, event_type, body) VALUES ($1, $2, $3)`, e.ID, e.Type, body)
	return err
}

// RunOutbox publishes stored events in order and deletes each one once the broker confirmed it.
// A crash between publishing and deleting sends that event again; consumers skip it with ProcessOnce.
// Several instances may run at once: SKIP LOCKED hands each row to only one of them.
// It returns when ctx ends or the broker connection is lost.
func RunOutbox(ctx context.Context, pool *pgxpool.Pool, b *Broker) error {
	for {
		n, err := relayBatch(ctx, pool, b)
		if err != nil {
			slog.Warn("outbox relay failed, will retry", "error", err)
		}
		if err == nil && n == outboxBatch {
			continue // more are waiting, don't sleep
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.Done():
			return ErrBrokerLost
		case <-time.After(outboxPoll):
		}
	}
}

func relayBatch(ctx context.Context, pool *pgxpool.Pool, b *Broker) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	type row struct {
		seq             int64
		eventID, evType string
		body            []byte
	}
	rows, _ := tx.Query(ctx, `SELECT seq, event_id, event_type, body FROM outbox ORDER BY seq LIMIT $1 FOR UPDATE SKIP LOCKED`, outboxBatch)
	pending, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.seq, &x.eventID, &x.evType, &x.body)
		return x, err
	})
	if err != nil {
		return 0, err
	}

	published := 0
	var pubErr error
	for _, x := range pending {
		if pubErr = b.publish(ctx, x.evType, x.eventID, x.body); pubErr != nil {
			break // stop here so the order is kept; the rest is tried next round
		}
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE seq = $1`, x.seq); err != nil {
			return published, err
		}
		published++
	}
	// Commit even after a publish error, so the events that did go out are not sent again.
	if err := tx.Commit(ctx); err != nil {
		return published, err
	}
	return published, pubErr
}
