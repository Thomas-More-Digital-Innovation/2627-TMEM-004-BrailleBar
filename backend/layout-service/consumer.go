package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

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

// Handle lays out a typed document or an OCR result and announces it with document.layout.completed.
// Returning an error makes the platform retry the event, then dead-letter it.
func (c *Consumer) Handle(ctx context.Context, e platform.Envelope) error {
	if e.Type == platform.DocumentOcrCompletedEvent {
		var p platform.DocumentOcrCompleted
		if err := e.Decode(&p); err != nil {
			return err
		}
		return c.layout(ctx, e, p.DocumentID, p.DeviceID, ocrText(p.Pages))
	}

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
	return c.layout(ctx, e, p.DocumentID, p.DeviceID, text)
}

// ocrText joins the lines of a printed page back into paragraphs, because the braille line is narrower: kept as they
// are, every printed line would leave a short leftover braille line. A paragraph ends at an empty line, at a line that
// ends a sentence, or at a line clearly shorter than the page's longest one (the last line of a paragraph, a heading).
// ponytail: a guess, not real paragraph detection; a long paragraph without a full stop gets joined to the next one.
func ocrText(pages []platform.OcrPage) string {
	var paragraphs []string
	current := ""
	end := func() {
		if current != "" {
			paragraphs = append(paragraphs, current)
			current = ""
		}
	}
	for _, page := range pages { // no end() between pages: a paragraph can continue on the next page
		longest := 0
		for _, line := range page.Lines {
			longest = max(longest, utf8.RuneCountInString(strings.TrimSpace(line.Text)))
		}
		for _, line := range page.Lines {
			text := strings.TrimSpace(line.Text)
			if text == "" {
				end()
				continue
			}
			first, _ := utf8.DecodeRuneInString(text)
			if word := strings.TrimRight(current, "-­‐"); word != current && unicode.IsLower(first) {
				current = word + text // "obstruc-" + "ției": a word split at the end of a printed line
			} else if current == "" {
				current = text
			} else {
				current += " " + text
			}
			last, _ := utf8.DecodeLastRuneInString(text)
			if strings.ContainsRune(".!?:", last) || utf8.RuneCountInString(text) < longest*7/10 {
				end()
			}
		}
	}
	end()
	return strings.Join(paragraphs, "\n")
}

func (c *Consumer) layout(ctx context.Context, e platform.Envelope, documentID, deviceID, text string) error {
	pages := Layout(text, c.CellsPerLine, c.LinesPerPage)
	pagesJSON, err := json.Marshal(pages)
	if err != nil {
		return err
	}
	sealedPages, err := platform.SealJSON(pagesJSON)
	if err != nil {
		return err
	}

	// ProcessOnce: a duplicate event does nothing; the row and the new event commit together.
	processed, err := platform.ProcessOnce(ctx, c.Pool, e.ID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO print_text (document_id, device_id, cells_per_line, pages) VALUES ($1, $2, $3, $4)`,
			documentID, deviceID, c.CellsPerLine, sealedPages,
		); err != nil {
			return err
		}
		event, err := platform.NewEnvelope(platform.DocumentLayoutCompletedEvent, platform.DocumentLayoutCompleted{
			DocumentID:   documentID,
			DeviceID:     deviceID,
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
		slog.Info("document laid out", "documentId", documentID, "eventId", e.ID, "pages", len(pages))
	} else {
		slog.Info("duplicate event skipped", "documentId", documentID, "eventId", e.ID)
	}
	return nil
}
