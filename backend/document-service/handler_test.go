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
	platform.LoadEnvFile("../../.env")
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	if err := platform.LoadEncryptionKey(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url+"?search_path=document")
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
	if strings.Contains(text, "hello") {
		t.Fatalf("text is stored unencrypted: %q", text)
	}
	if plain, err := platform.Open(text); err != nil || string(plain) != "hello world" {
		t.Fatalf("stored text opens as %q, %v", plain, err)
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

func getContent(h *Handler, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/documents/"+id+"/content", nil)
	req.SetPathValue("id", id) // normally the mux fills this from "{id}" in the route
	rec := httptest.NewRecorder()
	h.GetContent(rec, req)
	return rec
}

func TestGetContentReturnsText(t *testing.T) {
	h, pool := newTestHandler(t)

	created := submit(h, "dev-test", `{"text":"hello world"}`)
	var doc struct{ DocumentID string }
	if err := json.Unmarshal(created.Body.Bytes(), &doc); err != nil || doc.DocumentID == "" {
		t.Fatalf("could not create a document: %d %s", created.Code, created.Body.String())
	}
	// SubmitText also queued a document.uploaded event; remove it so it is never published.
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			`DELETE FROM outbox WHERE convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'documentId' = $1`, doc.DocumentID)
	})

	rec := getContent(h, doc.DocumentID)
	if rec.Code != 200 {
		t.Fatalf("got %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var got struct {
		Source string
		Text   string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Source != "text" || got.Text != "hello world" {
		t.Fatalf("got %+v, want source text and text %q", got, "hello world")
	}
}

func TestGetContentNotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	for _, id := range []string{
		"00000000-0000-4000-8000-000000000000", // valid uuid, but no such document
		"not-a-uuid",
		"",
	} {
		if rec := getContent(h, id); rec.Code != 404 {
			t.Errorf("id %q: got %d (%s), want 404", id, rec.Code, rec.Body.String())
		}
	}
}