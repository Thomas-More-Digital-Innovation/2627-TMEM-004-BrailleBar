package main

// Runs against a real Postgres, so it needs DATABASE_URL (skipped without it):
//   docker compose up -d postgres
//   DATABASE_URL=postgres://braillebar:braillebar@localhost:5433/braillebar?search_path=document go test ./...

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestHandler(t *testing.T) (*Handler, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
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
	return &Handler{Pool: pool}, pool
}

func submit(h *Handler, deviceID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/documents/text", strings.NewReader(body))
	if deviceID != "" {
		req.Header.Set("X-Device-Id", deviceID)
	}
	rec := httptest.NewRecorder()
	h.SubmitText(rec, req)
	return rec
}

func TestSubmitTextStoresDocumentAndEvent(t *testing.T) {
	h, pool := newTestHandler(t)
	ctx := context.Background()

	rec := submit(h, "dev-test", `{"text":"hello world"}`)
	if rec.Code != 201 {
		t.Fatalf("got %d (%s), want 201", rec.Code, rec.Body.String())
	}
	var resp struct{ DocumentID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.DocumentID == "" {
		t.Fatalf("bad response %q: %v", rec.Body.String(), err)
	}

	var text string
	if err := pool.QueryRow(ctx, `SELECT text FROM document WHERE id = $1`, resp.DocumentID).Scan(&text); err != nil {
		t.Fatalf("document not stored: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("stored %q, want %q", text, "hello world")
	}

	// The matching event must be waiting in the outbox. RunOutbox is not running in this test, so it stays there.
	var body []byte
	err := pool.QueryRow(ctx,
		`SELECT body FROM outbox WHERE event_type = $1 AND convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'documentId' = $2`,
		platform.DocumentUploadedEvent, resp.DocumentID,
	).Scan(&body)
	if err != nil {
		t.Fatalf("no document.uploaded event in the outbox: %v", err)
	}
	var env platform.Envelope
	var payload platform.DocumentUploaded
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if err := env.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.DeviceID != "dev-test" || payload.Source != "text" || payload.NeedsOcr {
		t.Fatalf("wrong event payload: %+v", payload)
	}
	pool.Exec(ctx, `DELETE FROM outbox WHERE event_id = $1`, env.ID) // keep it from being published later
}

func TestSubmitTextRejectsBadRequests(t *testing.T) {
	h, _ := newTestHandler(t)
	cases := []struct {
		name, deviceID, body string
		want                 int
	}{
		{"missing device id", "", `{"text":"hello"}`, 400},
		{"device id too long", strings.Repeat("x", 65), `{"text":"hello"}`, 400},
		{"empty text", "dev-test", `{"text":"   "}`, 400},
		{"not json", "dev-test", `hello`, 400},
		{"too large", "dev-test", `{"text":"` + strings.Repeat("a", 2<<20) + `"}`, 413},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if rec := submit(h, c.deviceID, c.body); rec.Code != c.want {
				t.Fatalf("got %d (%s), want %d", rec.Code, rec.Body.String(), c.want)
			}
		})
	}
}
