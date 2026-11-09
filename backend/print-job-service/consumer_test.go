package main

// Runs against a real Postgres (DATABASE_URL from the project's .env, without a search_path); skipped without it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	platform.LoadEnvFile("../../.env")
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	// own schema, so the running container's outbox loop doesn't publish (and delete) the test's events
	pool, err := pgxpool.New(ctx, url+"?search_path=print_job_test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS print_job_test`); err != nil {
		t.Fatal(err)
	}
	if err := platform.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// queueJob handles one document.layout.completed for a fresh document and returns its id.
func queueJob(t *testing.T, pool *pgxpool.Pool, pages [][]string) (documentID string, e platform.Envelope) {
	t.Helper()
	id, _ := platform.NewEnvelope("x", nil) // only for a fresh uuid
	documentID = id.ID
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM print_job WHERE document_id = $1`, documentID)
		pool.Exec(ctx, `DELETE FROM outbox WHERE convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'documentId' = $1`, documentID)
	})
	e, err := platform.NewEnvelope(platform.DocumentLayoutCompletedEvent, platform.DocumentLayoutCompleted{
		DocumentID: documentID, DeviceID: "dev-test", CellsPerLine: 30, Pages: pages,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Consumer{Pool: pool}).Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	return documentID, e
}

func TestHandleQueuesJobAndEventOnce(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	pages := [][]string{{"hello world", ""}, {"second page"}}
	documentID, e := queueJob(t, pool, pages)
	if err := (&Consumer{Pool: pool}).Handle(ctx, e); err != nil { // the same event delivered twice
		t.Fatalf("duplicate delivery failed: %v", err)
	}

	var jobID, state string
	var pageCount, jobs int
	pool.QueryRow(ctx, `SELECT count(*) FROM print_job WHERE document_id = $1`, documentID).Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("print_job rows = %d, want 1", jobs)
	}
	pool.QueryRow(ctx, `SELECT id::text, state, page_count FROM print_job WHERE document_id = $1`, documentID).Scan(&jobID, &state, &pageCount)
	if state != "queued" || pageCount != 2 {
		t.Fatalf("job state %q, pageCount %d; want queued, 2", state, pageCount)
	}

	var bodies [][]byte
	r, err := pool.Query(ctx, `SELECT body FROM outbox WHERE event_type = $1 AND convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'documentId' = $2`,
		platform.PrintJobCreatedEvent, documentID)
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var b []byte
		r.Scan(&b)
		bodies = append(bodies, b)
	}
	if len(bodies) != 1 {
		t.Fatalf("print.job.created events = %d, want 1", len(bodies))
	}
	var env platform.Envelope
	var p platform.PrintJobCreated
	json.Unmarshal(bodies[0], &env)
	env.Decode(&p)
	if p.JobID != jobID || p.DeviceID != "dev-test" || !reflect.DeepEqual(p.Pages, pages) {
		t.Fatalf("wrong event payload: %+v", p)
	}
}

func TestGetJobOnlyForItsDevice(t *testing.T) {
	pool := newTestPool(t)
	documentID, _ := queueJob(t, pool, [][]string{{"hi"}})
	var jobID string
	pool.QueryRow(context.Background(), `SELECT id::text FROM print_job WHERE document_id = $1`, documentID).Scan(&jobID)

	h := &Handler{Pool: pool}
	get := func(id, device string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/jobs/"+id, nil)
		req.SetPathValue("id", id)
		req.Header.Set("X-Device-Id", device)
		rec := httptest.NewRecorder()
		h.GetJob(rec, req)
		return rec
	}

	rec := get(jobID, "dev-test")
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || body["state"] != "queued" || body["pageCount"] != float64(1) {
		t.Fatalf("own job: %d %s", rec.Code, rec.Body)
	}
	if rec := get(jobID, "dev-other"); rec.Code != http.StatusNotFound {
		t.Fatalf("other device got %d, want 404", rec.Code)
	}
	if rec := get("not-a-uuid", "dev-test"); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id got %d, want 404", rec.Code)
	}
}

// jobFor queues a job and returns its id plus a helper that calls a handler as the given device.
func jobFor(t *testing.T, pool *pgxpool.Pool, pages [][]string) (string, func(http.HandlerFunc, string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	documentID, _ := queueJob(t, pool, pages)
	var jobID string
	pool.QueryRow(context.Background(), `SELECT id::text FROM print_job WHERE document_id = $1`, documentID).Scan(&jobID)
	call := func(fn http.HandlerFunc, method, device, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/jobs/"+jobID, strings.NewReader(body))
		req.SetPathValue("id", jobID)
		req.Header.Set("X-Device-Id", device)
		rec := httptest.NewRecorder()
		fn(rec, req)
		return rec
	}
	return jobID, call
}

func TestOpenJobs(t *testing.T) {
	pool := newTestPool(t)
	jobID, call := jobFor(t, pool, [][]string{{"a"}})
	h := &Handler{Pool: pool}
	listed := func(device string) bool {
		rec := call(h.OpenJobs, "GET", device, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("open jobs: %d %s", rec.Code, rec.Body)
		}
		return strings.Contains(rec.Body.String(), jobID)
	}

	if !listed("dev-test") {
		t.Fatal("queued job is not listed")
	}
	if listed("dev-other") {
		t.Fatal("job listed for another device")
	}
	call(h.ReportStatus, "POST", "dev-test", `{"state":"done","page":1}`)
	if listed("dev-test") {
		t.Fatal("finished job is still listed")
	}
}

func TestGetPages(t *testing.T) {
	pool := newTestPool(t)
	pages := [][]string{{"hello", ""}, {"world"}}
	_, call := jobFor(t, pool, pages)
	h := &Handler{Pool: pool}

	rec := call(h.GetPages, "GET", "dev-test", "")
	var body struct{ Pages [][]string }
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || !reflect.DeepEqual(body.Pages, pages) {
		t.Fatalf("own pages: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h.GetPages, "GET", "dev-other", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("other device got %d, want 404", rec.Code)
	}
}

func TestReportStatus(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	jobID, call := jobFor(t, pool, [][]string{{"a"}, {"b"}})
	h := &Handler{Pool: pool}

	cases := []struct {
		name, device, body string
		want               int
	}{
		{"printing page 1", "dev-test", `{"state":"printing","page":1}`, http.StatusNoContent},
		{"retry of the same report", "dev-test", `{"state":"printing","page":1}`, http.StatusNoContent},
		{"other device", "dev-other", `{"state":"printing","page":2}`, http.StatusNotFound},
		{"agent can't set queued", "dev-test", `{"state":"queued","page":0}`, http.StatusBadRequest},
		{"page past the end", "dev-test", `{"state":"printing","page":3}`, http.StatusBadRequest},
		{"bad json", "dev-test", `{`, http.StatusBadRequest},
		{"earlier state on the same page", "dev-test", `{"state":"sending","page":1}`, http.StatusConflict},
		{"earlier page", "dev-test", `{"state":"printing","page":0}`, http.StatusConflict},
		{"next page starts with sending", "dev-test", `{"state":"sending","page":2}`, http.StatusNoContent},
		{"failed", "dev-test", `{"state":"failed","page":2,"reason":"paper jam"}`, http.StatusNoContent},
		{"finished job is final", "dev-test", `{"state":"printing","page":1}`, http.StatusConflict},
	}
	for _, c := range cases {
		if rec := call(h.ReportStatus, "POST", c.device, c.body); rec.Code != c.want {
			t.Fatalf("%s: status %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body)
		}
	}

	rec := call(h.GetJob, "GET", "dev-test", "")
	var job map[string]any
	json.Unmarshal(rec.Body.Bytes(), &job)
	if job["state"] != "failed" || job["page"] != float64(2) || job["reason"] != "paper jam" {
		t.Fatalf("job is %v, want failed on page 2 with reason paper jam", job)
	}
	var states []string
	r, err := pool.Query(ctx, `SELECT convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'state' FROM outbox
		WHERE event_type = $1 AND convert_from(body, 'UTF8')::jsonb -> 'payload' ->> 'jobId' = $2 ORDER BY seq`,
		platform.PrintJobStatusEvent, jobID)
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var s string
		r.Scan(&s)
		states = append(states, s)
	}
	if !reflect.DeepEqual(states, []string{"printing", "printing", "sending", "failed"}) {
		t.Fatalf("print.job.status events %v, want [printing printing sending failed]", states)
	}
}
