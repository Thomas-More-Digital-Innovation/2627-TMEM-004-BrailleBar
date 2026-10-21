package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// print_job is the Readme's PrintJob: one print of one laid-out document, and how far it got.
const printJobTable = `CREATE TABLE IF NOT EXISTS print_job (
	id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	document_id uuid NOT NULL,
	device_id   text NOT NULL,
	state       text NOT NULL DEFAULT 'queued',
	page        int  NOT NULL DEFAULT 0,
	page_count  int  NOT NULL,
	pages       jsonb NOT NULL,
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now()
)`

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, printJobTable)
	return err
}
