package platform

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Each consuming service keeps its own processed_event table in its own schema (see Readme, Data Model).
const processedEventTable = `CREATE TABLE IF NOT EXISTS processed_event (
	event_id     text PRIMARY KEY,
	processed_at timestamptz NOT NULL DEFAULT now()
)`

// Migrate creates the platform tables (processed_event and outbox) in the service's schema.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	for _, ddl := range []string{processedEventTable, outboxTable} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

// ProcessOnce runs fn at most once per event id. The id is stored in the same transaction as fn's own
// writes, so either both happen or neither does: a crash can never leave an event half processed.
// It returns false when the event was already processed (fn is not called).
func ProcessOnce(ctx context.Context, pool *pgxpool.Pool, eventID string, fn func(tx pgx.Tx) error) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) // no-op after a successful commit

	// A concurrent duplicate waits here for the first transaction to finish, then sees the conflict.
	tag, err := tx.Exec(ctx, `INSERT INTO processed_event (event_id) VALUES ($1) ON CONFLICT DO NOTHING`, eventID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := fn(tx); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
