package main

// Runs against a real Postgres (DATABASE_URL from the project's .env, without a search_path); skipped without it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestConsumer uses a fake Document Service that answers with the given text.
func newTestConsumer(t *testing.T, text string) (*Consumer, *pgxpool.Pool) {
	t.Helper()
	platform.LoadEnvFile("../../.env")
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url+"?search_path=layout")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := platform.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"source": "text", "text": text})
	}))
	t.Cleanup(docs.Close)

	return &Consumer{
		Pool:         pool,
		Docs:         &DocClient{BaseURL: docs.URL, HTTP: docs.Client()},
		CellsPerLine: 30,
		LinesPerPage: 17,
	}, pool
}

func uploadedEvent(t *testing.T, documentID string, needsOcr bool) platform.Envelope {
	t.Helper()
	e, err := platform.NewEnvelope(platform.DocumentUploadedEvent, platform.DocumentUploaded{
		DocumentID: documentID, DeviceID: "dev-test", Source: "text", NeedsOcr: needsOcr,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func cleanup(t *testing.T, pool *pgxpool.Pool, documentID string) {
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM print_text WHERE document_id = $1`, documentID)
		pool.Exec(ctx, `DELETE FROM outbox WHERE convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'documentId' = $1`, documentID)
	})
}

func TestHandleStoresPagesAndEventOnce(t *testing.T) {
	c, pool := newTestConsumer(t, "hello world")
	ctx := context.Background()
	documentID := "11111111-1111-4111-8111-" + randomSuffix(t)
	cleanup(t, pool, documentID)

	e := uploadedEvent(t, documentID, false)
	if err := c.Handle(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := c.Handle(ctx, e); err != nil { // the same event delivered twice
		t.Fatalf("duplicate delivery failed: %v", err)
	}

	var rows int
	pool.QueryRow(ctx, `SELECT count(*) FROM print_text WHERE document_id = $1`, documentID).Scan(&rows)
	if rows != 1 {
		t.Fatalf("print_text rows = %d, want 1", rows)
	}

	var bodies [][]byte
	r, err := pool.Query(ctx, `SELECT body FROM outbox WHERE event_type = $1 AND convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'documentId' = $2`,
		platform.DocumentLayoutCompletedEvent, documentID)
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var b []byte
		r.Scan(&b)
		bodies = append(bodies, b)
	}
	if len(bodies) != 1 {
		t.Fatalf("document.layout.completed events = %d, want 1", len(bodies))
	}
	var env platform.Envelope
	var p platform.DocumentLayoutCompleted
	json.Unmarshal(bodies[0], &env)
	env.Decode(&p)
	if !reflect.DeepEqual(p.Pages, [][]string{{"hello world"}}) || p.CellsPerLine != 30 || p.DeviceID != "dev-test" {
		t.Fatalf("wrong event payload: %+v", p)
	}
}

func TestHandleSkipsDocumentsThatNeedOcr(t *testing.T) {
	c, pool := newTestConsumer(t, "should not be used")
	ctx := context.Background()
	documentID := "22222222-2222-4222-8222-" + randomSuffix(t)
	cleanup(t, pool, documentID)

	if err := c.Handle(ctx, uploadedEvent(t, documentID, true)); err != nil {
		t.Fatal(err)
	}
	var rows int
	pool.QueryRow(ctx, `SELECT count(*) FROM print_text WHERE document_id = $1`, documentID).Scan(&rows)
	if rows != 0 {
		t.Fatalf("a document that needs OCR was laid out")
	}
}

// randomSuffix gives the last 12 hex digits of a uuid, so test runs never collide.
func randomSuffix(t *testing.T) string {
	e, err := platform.NewEnvelope("x", nil)
	if err != nil {
		t.Fatal(err)
	}
	return e.ID[len(e.ID)-12:]
}
