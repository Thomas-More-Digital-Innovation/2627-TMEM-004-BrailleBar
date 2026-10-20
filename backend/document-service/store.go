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

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, documentTable)
	return err
}
