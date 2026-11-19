package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5/pgxpool"
)

func zipWith(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	if _, err := z.Create(name); err != nil {
		t.Fatal(err)
	}
	z.Close()
	return buf.Bytes()
}

func upload(h *Handler, deviceID string, data []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/documents/file", bytes.NewReader(data))
	req.Header.Set("X-Device-Id", deviceID)
	// a lying header must make no difference
	req.Header.Set("Content-Type", "application/pdf")
	rec := httptest.NewRecorder()
	h.SubmitFile(rec, req)
	return rec
}

func cleanupDocument(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM outbox WHERE convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'documentId' = $1`, id)
		pool.Exec(ctx, `DELETE FROM document WHERE id = $1`, id)
	})
}

func TestSubmitFile(t *testing.T) {
	h, pool := newTestHandler(t)
	ctx := context.Background()
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("private picture")...)

	cases := []struct {
		name, source, contentType string
		data                      []byte
	}{
		{"pdf", "pdf", "application/pdf", []byte("%PDF-1.7 private letter")},
		{"png", "image", "image/png", png},
		{"jpeg", "image", "image/jpeg", []byte("\xFF\xD8\xFF private picture")},
		{"docx", "word", docxContentType, zipWith(t, "word/document.xml")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := upload(h, "dev-test", c.data)
			if rec.Code != 201 {
				t.Fatalf("got %d (%s), want 201", rec.Code, rec.Body)
			}
			var doc struct{ DocumentID string }
			json.Unmarshal(rec.Body.Bytes(), &doc)
			cleanupDocument(t, pool, doc.DocumentID)

			var stored string
			pool.QueryRow(ctx, `SELECT data FROM document_file WHERE document_id = $1`, doc.DocumentID).Scan(&stored)
			if strings.Contains(stored, "private") || !strings.HasPrefix(stored, "enc:") {
				t.Fatalf("file is stored unencrypted: %.40q", stored)
			}

			var body []byte
			pool.QueryRow(ctx, `SELECT body FROM outbox WHERE convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'documentId' = $1`, doc.DocumentID).Scan(&body)
			var env platform.Envelope
			var p platform.DocumentUploaded
			json.Unmarshal(body, &env)
			env.Decode(&p)
			if p.Source != c.source || !p.NeedsOcr || p.DeviceID != "dev-test" {
				t.Fatalf("wrong event: %+v", p)
			}

			req := httptest.NewRequest("GET", "/documents/"+doc.DocumentID+"/file", nil)
			req.SetPathValue("id", doc.DocumentID)
			got := httptest.NewRecorder()
			h.GetFile(got, req)
			if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), c.data) || got.Header().Get("Content-Type") != c.contentType {
				t.Fatalf("GetFile: %d %q %q", got.Code, got.Header().Get("Content-Type"), got.Body.Bytes())
			}
		})
	}
}

func TestSubmitFileRejects(t *testing.T) {
	h, _ := newTestHandler(t)
	cases := []struct {
		name string
		data []byte
		want int
	}{
		{"empty", nil, 400},
		{"program", []byte("MZ\x90\x00 pretends to be a pdf"), 415},
		{"plain text", []byte("hello"), 415},
		{"zip that is no docx", zipWith(t, "evil.exe"), 415},
		{"too large", append([]byte("%PDF-"), make([]byte, maxFileBytes)...), 413},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if rec := upload(h, "dev-test", c.data); rec.Code != c.want {
				t.Fatalf("got %d (%s), want %d", rec.Code, rec.Body, c.want)
			}
		})
	}
}

func TestExpiredDocumentsAndTheirFilesAreDeleted(t *testing.T) {
	h, pool := newTestHandler(t)
	ctx := context.Background()

	id := func(data string) string {
		var doc struct{ DocumentID string }
		json.Unmarshal(upload(h, "dev-test", []byte(data)).Body.Bytes(), &doc)
		cleanupDocument(t, pool, doc.DocumentID)
		return doc.DocumentID
	}
	old, fresh := id("%PDF-old"), id("%PDF-fresh")
	// far older than any real row, because this schema is shared with the running service
	pool.Exec(ctx, `UPDATE document SET created_at = now() - interval '4000 days' WHERE id = $1`, old)

	if _, err := platform.DeleteExpired(ctx, pool, "document", 3650); err != nil {
		t.Fatal(err)
	}
	count := func(id string) (n int) {
		pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM document WHERE id = $1) + (SELECT count(*) FROM document_file WHERE document_id = $1)`, id).Scan(&n)
		return
	}
	if n := count(old); n != 0 {
		t.Fatalf("expired document left %d rows", n)
	}
	if n := count(fresh); n != 2 {
		t.Fatalf("fresh document has %d rows, want 2", n)
	}
}
