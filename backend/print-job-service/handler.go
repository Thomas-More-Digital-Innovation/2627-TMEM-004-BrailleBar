package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"uuid"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	Pool *pgxpool.Pool
}

// GetJob answers with a job's progress. A job of another device is a 404, so ids can't be probed.
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	var documentID, state, reason string
	var page, pageCount int
	err := h.Pool.QueryRow(r.Context(),
		`SELECT document_id::text, state, page, page_count, reason FROM print_job WHERE id = $1 AND device_id = $2`,
		id, r.Header.Get("X-Device-Id"),
	).Scan(&documentID, &state, &page, &pageCount, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, "get job", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"jobId": id, "documentId": documentID, "deviceId": r.Header.Get("X-Device-Id"),
		"state": state, "page": page, "pageCount": pageCount, "reason": reason,
	})
}

// OpenJobs lists this device's unfinished jobs, oldest first, so an agent that was offline can pick them up.
func (h *Handler) OpenJobs(w http.ResponseWriter, r *http.Request) {
	device := r.Header.Get("X-Device-Id")
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id::text, document_id::text, state, page, page_count, reason FROM print_job
		 WHERE device_id = $1 AND state NOT IN ('done', 'failed', 'cancelled') ORDER BY created_at`, device)
	if err != nil {
		serverError(w, "list jobs", err)
		return
	}
	defer rows.Close()
	jobs := []map[string]any{}
	for rows.Next() {
		var id, documentID, state, reason string
		var page, pageCount int
		if err := rows.Scan(&id, &documentID, &state, &page, &pageCount, &reason); err != nil {
			serverError(w, "scan job", err)
			return
		}
		jobs = append(jobs, map[string]any{
			"jobId": id, "documentId": documentID, "deviceId": device,
			"state": state, "page": page, "pageCount": pageCount, "reason": reason,
		})
	}
	if err := rows.Err(); err != nil {
		serverError(w, "list jobs", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
}

// GetPages gives the agent the laid-out pages to send to the printer.
func (h *Handler) GetPages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	var pages json.RawMessage
	err := h.Pool.QueryRow(r.Context(),
		`SELECT pages FROM print_job WHERE id = $1 AND device_id = $2`, id, r.Header.Get("X-Device-Id"),
	).Scan(&pages)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, "get pages", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"jobId": id, "pages": pages})
}

// stateOrder ranks the states within one page; queued (0) is only ever set by this service.
// A page can go sending -> printing, then the next page starts with sending again.
var stateOrder = map[string]int{"queued": 0, "sending": 1, "printing": 2, "done": 3, "failed": 3, "cancelled": 3}

type statusRequest struct {
	State  string `json:"state"`
	Page   int    `json:"page"`
	Reason string `json:"reason"`
}

// ReportStatus stores the agent's progress and announces it with print.job.status.
// A job only moves forward: an earlier page, an earlier state on the same page or a change to a
// finished job (done, failed, cancelled) is a 409.
func (h *Handler) ReportStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	deviceID := r.Header.Get("X-Device-Id")
	var req statusRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if stateOrder[req.State] == 0 || req.Page < 0 || len(req.Reason) > 200 {
		http.Error(w, "invalid state, page or reason", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		serverError(w, "begin", err)
		return
	}
	defer tx.Rollback(ctx)

	var documentID, current string
	var page, pageCount int
	err = tx.QueryRow(ctx,
		`SELECT document_id::text, state, page, page_count FROM print_job WHERE id = $1 AND device_id = $2 FOR UPDATE`,
		id, deviceID,
	).Scan(&documentID, &current, &page, &pageCount)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, "select job", err)
		return
	}
	if stateOrder[current] == 3 {
		http.Error(w, "job is already "+current, http.StatusConflict)
		return
	}
	// a repeat of the current state is allowed, so an agent can safely retry a report
	if req.Page < page || req.Page == page && stateOrder[req.State] < stateOrder[current] {
		http.Error(w, "job is already "+current+" on page "+strconv.Itoa(page), http.StatusConflict)
		return
	}
	if req.Page > pageCount {
		http.Error(w, "page is past the last page", http.StatusBadRequest)
		return
	}

	if _, err := tx.Exec(ctx,
		`UPDATE print_job SET state = $2, page = $3, reason = $4, updated_at = now() WHERE id = $1`, id, req.State, req.Page, req.Reason,
	); err != nil {
		serverError(w, "update job", err)
		return
	}
	event, err := platform.NewEnvelope(platform.PrintJobStatusEvent, platform.PrintJobStatus{
		JobID: id, DocumentID: documentID, DeviceID: deviceID,
		State: req.State, Page: req.Page, PageCount: pageCount, Reason: req.Reason,
	})
	if err != nil {
		serverError(w, "new envelope", err)
		return
	}
	if err := platform.Enqueue(ctx, tx, event); err != nil {
		serverError(w, "enqueue", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, "commit", err)
		return
	}
	slog.Info("job status", "jobId", id, "state", req.State, "page", req.Page, "pageCount", pageCount)
	w.WriteHeader(http.StatusNoContent)
}

func serverError(w http.ResponseWriter, step string, err error) {
	slog.Error("print job request failed", "step", step, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
