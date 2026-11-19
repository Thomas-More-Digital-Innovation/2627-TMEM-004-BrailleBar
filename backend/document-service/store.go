package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

const documentTable = `CREATE TABLE IF NOT EXISTS document (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  device_id  text NOT NULL,
  source     text NOT NULL,
  text       text,
  created_at timestamptz NOT NULL DEFAULT now()
)`

// ponytail: files live in Postgres so they commit with their event and share its backups; move to object storage if files get big.
const documentFileTable = `CREATE TABLE IF NOT EXISTS document_file (
  document_id  uuid PRIMARY KEY REFERENCES document (id) ON DELETE CASCADE,
  content_type text NOT NULL,
  data         text NOT NULL
)`

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	for _, table := range []string{documentTable, documentFileTable} {
		if _, err := pool.Exec(ctx, table); err != nil {
			return err
		}
	}
	return nil
}
