package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// print_text is the Readme's PrintText: the laid-out pages of one document.
const printTextTable = `CREATE TABLE IF NOT EXISTS print_text (
	document_id    uuid PRIMARY KEY,
	device_id      text NOT NULL,
	cells_per_line int  NOT NULL,
	pages          jsonb NOT NULL,
	created_at     timestamptz NOT NULL DEFAULT now()
)`

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, printTextTable)
	return err
}
