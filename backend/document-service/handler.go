package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"uuid"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxBodyBytes = 1 << 20  // 1 MB is far more text than anyone types
	maxFileBytes = 20 << 20 // a long scanned PDF
)

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

	if text != nil { // a file has no text until OCR
		plain, err := platform.Open(*text)
		if err != nil {
			serverError(w, "decrypt text", err)
			return
		}
		s := string(plain)
		text = &s
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"source": source, "text": text})
}

// GetFile hands the original upload to the OCR Service. Internal: the gateway has no route to it.
func (h *Handler) GetFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "id is not an uuid", http.StatusNotFound)
		return
	}
	var contentType, sealed string
	err := h.Pool.QueryRow(r.Context(),
		`SELECT content_type, data FROM document_file WHERE document_id = $1`, id,
	).Scan(&contentType, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, "select file", err)
		return
	}
	data, err := platform.Open(sealed)
	if err != nil {
		serverError(w, "decrypt file", err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(data)
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

	sealed, err := platform.Seal([]byte(req.Text))
	if err != nil {
		serverError(w, "encrypt text", err)
		return
	}
	h.store(w, r, deviceID, "text", &sealed, "", "") // typed text skips OCR
}

// SubmitFile stores a PDF, Word file or image, sent as the raw request body, and announces it for OCR.
// The type comes from the bytes themselves: the client's Content-Type and file name can't be trusted.
func (h *Handler) SubmitFile(w http.ResponseWriter, r *http.Request) {
	deviceID := r.Header.Get("X-Device-Id")
	if deviceID == "" || len(deviceID) > 64 {
		http.Error(w, "missing or invalid X-Device-Id header", http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFileBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "file is too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "could not read the file", http.StatusBadRequest)
		return
	}
	if len(data) == 0 {
		http.Error(w, "file is empty", http.StatusBadRequest)
		return
	}
	source, contentType := detectFile(data)
	if source == "" {
		http.Error(w, "only PDF, Word (docx), JPEG and PNG files are supported", http.StatusUnsupportedMediaType)
		return
	}
	sealed, err := platform.Seal(data)
	if err != nil {
		serverError(w, "encrypt file", err)
		return
	}
	h.store(w, r, deviceID, source, nil, contentType, sealed)
}

const docxContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// detectFile returns the document source and content type, or "" for a type we don't accept.
func detectFile(data []byte) (source, contentType string) {
	switch ct := http.DetectContentType(data); ct {
	case "application/pdf":
		return "pdf", ct
	case "image/jpeg", "image/png":
		return "image", ct
	case "application/zip": // a docx is a zip with this one file in it; only the file list is read, nothing is unpacked
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", ""
		}
		for _, f := range z.File {
			if f.Name == "word/document.xml" {
				return "word", docxContentType
			}
		}
	}
	return "", ""
}

// store saves the document and its event in one transaction: both happen or neither does. A file (non-empty
// sealedFile) needs OCR; text doesn't.
func (h *Handler) store(w http.ResponseWriter, r *http.Request, deviceID, source string, sealedText *string, contentType, sealedFile string) {
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		serverError(w, "begin transaction", err)
		return
	}
	defer tx.Rollback(ctx) // does nothing once Commit succeeded

	var documentID string
	err = tx.QueryRow(ctx,
		`INSERT INTO document (device_id, source, text) VALUES ($1, $2, $3) RETURNING id`,
		deviceID, source, sealedText,
	).Scan(&documentID)
	if err != nil {
		serverError(w, "insert document", err)
		return
	}
	if sealedFile != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO document_file (document_id, content_type, data) VALUES ($1, $2, $3)`,
			documentID, contentType, sealedFile); err != nil {
			serverError(w, "insert file", err)
			return
		}
	}

	event, err := platform.NewEnvelope(platform.DocumentUploadedEvent, platform.DocumentUploaded{
		DocumentID: documentID,
		DeviceID:   deviceID,
		Source:     source,
		NeedsOcr:   sealedFile != "",
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
	slog.Info("document stored", "documentId", documentID, "eventId", event.ID, "source", source) // ids only, never the content

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"documentId": documentID})
}

// The real error goes to the log; the caller only gets a generic message.
func serverError(w http.ResponseWriter, step string, err error) {
	slog.Error("request failed", "step", step, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
