package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Consumer struct {
	Pool *pgxpool.Pool
}

// Handle queues a print job for a laid-out document and announces it with print.job.created.
// Returning an error makes the platform retry the event, then dead-letter it.
func (c *Consumer) Handle(ctx context.Context, e platform.Envelope) error {
	var p platform.DocumentLayoutCompleted
	if err := e.Decode(&p); err != nil {
		return err
	}
	pagesJSON, err := json.Marshal(p.Pages)
	if err != nil {
		return err
	}

	// ProcessOnce: a duplicate document.layout.completed does nothing; the job and the new event commit together.
	var jobID string
	processed, err := platform.ProcessOnce(ctx, c.Pool, e.ID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO print_job (document_id, device_id, page_count, pages) VALUES ($1, $2, $3, $4) RETURNING id`,
			p.DocumentID, p.DeviceID, len(p.Pages), string(pagesJSON),
		).Scan(&jobID); err != nil {
			return err
		}
		event, err := platform.NewEnvelope(platform.PrintJobCreatedEvent, platform.PrintJobCreated{
			JobID:      jobID,
			DocumentID: p.DocumentID,
			DeviceID:   p.DeviceID,
			Pages:      p.Pages,
		})
		if err != nil {
			return err
		}
		return platform.Enqueue(ctx, tx, event)
	})
	if err != nil {
		return err
	}
	if processed {
		slog.Info("print job queued", "jobId", jobID, "documentId", p.DocumentID, "eventId", e.ID, "pages", len(p.Pages))
	} else {
		slog.Info("duplicate event skipped", "documentId", p.DocumentID, "eventId", e.ID)
	}
	return nil
}
