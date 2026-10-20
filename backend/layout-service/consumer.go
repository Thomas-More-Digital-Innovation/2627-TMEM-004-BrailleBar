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
	Pool         *pgxpool.Pool
	Docs         *DocClient
	CellsPerLine int
	LinesPerPage int
}

// Handle lays out a typed document and announces it with document.layout.completed.
// Returning an error makes the platform retry the event, then dead-letter it.
func (c *Consumer) Handle(ctx context.Context, e platform.Envelope) error {
	var p platform.DocumentUploaded
	if err := e.Decode(&p); err != nil {
		return err
	}
	if p.NeedsOcr {
		slog.Info("skipped, waits for OCR", "documentId", p.DocumentID, "eventId", e.ID)
		return nil // laid out later from document.ocr.completed
	}

	text, err := c.Docs.FetchText(ctx, p.DocumentID) // network call stays outside the transaction
	if err != nil {
		return err
	}
	pages := Layout(text, c.CellsPerLine, c.LinesPerPage)
	pagesJSON, err := json.Marshal(pages)
	if err != nil {
		return err
	}

	// ProcessOnce: a duplicate document.uploaded does nothing; the row and the new event commit together.
	processed, err := platform.ProcessOnce(ctx, c.Pool, e.ID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO print_text (document_id, device_id, cells_per_line, pages) VALUES ($1, $2, $3, $4)`,
			p.DocumentID, p.DeviceID, c.CellsPerLine, string(pagesJSON),
		); err != nil {
			return err
		}
		event, err := platform.NewEnvelope(platform.DocumentLayoutCompletedEvent, platform.DocumentLayoutCompleted{
			DocumentID:   p.DocumentID,
			DeviceID:     p.DeviceID,
			CellsPerLine: c.CellsPerLine,
			Pages:        pages,
		})
		if err != nil {
			return err
		}
		return platform.Enqueue(ctx, tx, event)
	})
	if err != nil {
		return err
	}
	// ids and counts only, never the text
	if processed {
		slog.Info("document laid out", "documentId", p.DocumentID, "eventId", e.ID, "pages", len(pages))
	} else {
		slog.Info("duplicate event skipped", "documentId", p.DocumentID, "eventId", e.ID)
	}
	return nil
}
