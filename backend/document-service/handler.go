package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"uuid"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxBodyBytes = 1 << 20 // 1 MB is far more text than anyone types

type Handler struct {
	Pool *pgxpool.Pool
}

type submitTextRequest struct {
	Text string `json:"text"`
}

func (h *Handler) GetContent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "id is not an uuid", http.StatusNotFound)
		return
	}
	var source string
	var text *string
	err := h.Pool.QueryRow(r.Context(),
		`SELECT source, text FROM document WHERE id = $1`, id,
	).Scan(&source, &text)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, "select document", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"source": source, "text": text})
}

// SubmitText stores typed text and announces it with document.uploaded.
func (h *Handler) SubmitText(w http.ResponseWriter, r *http.Request) {
	// 1. Who is asking
	deviceID := r.Header.Get("X-Device-Id")
	if deviceID == "" || len(deviceID) > 64 {
		http.Error(w, "missing or invalid X-Device-Id header", http.StatusBadRequest)
		return
	}

	// 2. Read and check the body, never more than maxBodyBytes
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req submitTextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "text is too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, `body must be JSON like {"text": "..."}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		http.Error(w, "text is empty", http.StatusBadRequest)
		return
	}

	// 3. Store the document and its event in one transaction: both happen or neither does
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		serverError(w, "begin transaction", err)
		return
	}
	defer tx.Rollback(ctx) // does nothing once Commit succeeded

	var documentID string
	err = tx.QueryRow(ctx,
		`INSERT INTO document (device_id, source, text) VALUES ($1, 'text', $2) RETURNING id`,
		deviceID, req.Text,
	).Scan(&documentID)
	if err != nil {
		serverError(w, "insert document", err)
		return
	}

	event, err := platform.NewEnvelope(platform.DocumentUploadedEvent, platform.DocumentUploaded{
		DocumentID: documentID,
		DeviceID:   deviceID,
		Source:     "text",
		NeedsOcr:   false, // typed text skips OCR
	})
	if err != nil {
		serverError(w, "build event", err)
		return
	}
	if err := platform.Enqueue(ctx, tx, event); err != nil {
		serverError(w, "enqueue event", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, "commit", err)
		return
	}
	slog.Info("document stored", "documentId", documentID, "eventId", event.ID) // ids only, never the text

	// 4. Answer with the new id
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"documentId": documentID})
}

// The real error goes to the log; the caller only gets a generic message.
func serverError(w http.ResponseWriter, step string, err error) {
	slog.Error("submit text failed", "step", step, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
