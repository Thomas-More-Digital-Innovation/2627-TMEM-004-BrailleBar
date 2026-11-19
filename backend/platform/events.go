// Package platform is the plumbing every service shares: the event envelope, the RabbitMQ
// helpers and the duplicate-event check. It holds no business logic.
package platform

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"
)

// Event types, also the RabbitMQ routing keys.
const (
	DocumentUploadedEvent        = "document.uploaded"
	DocumentOcrCompletedEvent    = "document.ocr.completed"
	DocumentLayoutCompletedEvent = "document.layout.completed"
	PrintJobCreatedEvent         = "print.job.created"
	PrintJobStatusEvent          = "print.job.status"
)

// Envelope wraps every event. ID is what consumers store to skip duplicates.
type Envelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Payload    json.RawMessage `json:"payload"`
	// W3C trace context of the request that caused this event; set by Enqueue and Publish.
	Traceparent string `json:"traceparent,omitempty"`
}

func NewEnvelope(eventType string, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{ID: newID(), Type: eventType, OccurredAt: time.Now().UTC(), Payload: raw}, nil
}

// Decode fills v, a pointer to the payload struct of this event type.
func (e Envelope) Decode(v any) error { return json.Unmarshal(e.Payload, v) }

// UUID v4 from crypto/rand, so no dependency is needed for it.
func newID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Payloads. These must match contracts/events.schema.json (checked by contracts_test.go).
// Slices such as Pages and Lines must be empty, not nil: nil marshals to null, which the schema rejects.

type DocumentUploaded struct {
	DocumentID string `json:"documentId"`
	DeviceID   string `json:"deviceId"`
	Source     string `json:"source"` // text, pdf, word, image or scan
	NeedsOcr   bool   `json:"needsOcr"`
}

type OcrBox struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type OcrWord struct {
	Text string `json:"text"`
	Box  OcrBox `json:"box"`
}

type OcrLine struct {
	Text  string    `json:"text"`
	Box   OcrBox    `json:"box"`
	Words []OcrWord `json:"words"`
}

type OcrPage struct {
	Page   int       `json:"page"`
	Width  float64   `json:"width"`
	Height float64   `json:"height"`
	Lines  []OcrLine `json:"lines"`
}

type DocumentOcrCompleted struct {
	DocumentID string    `json:"documentId"`
	DeviceID   string    `json:"deviceId"`
	Pages      []OcrPage `json:"pages"`
}

type DocumentLayoutCompleted struct {
	DocumentID   string     `json:"documentId"`
	DeviceID     string     `json:"deviceId"`
	CellsPerLine int        `json:"cellsPerLine"`
	Pages        [][]string `json:"pages"`
}

type PrintJobCreated struct {
	JobID      string     `json:"jobId"`
	DocumentID string     `json:"documentId"`
	DeviceID   string     `json:"deviceId"`
	Pages      [][]string `json:"pages"`
}

type PrintJobStatus struct {
	JobID      string `json:"jobId"`
	DocumentID string `json:"documentId"`
	DeviceID   string `json:"deviceId"`
	State      string `json:"state"` // queued, sending, printing, done, failed or cancelled
	Page       int    `json:"page"`
	PageCount  int    `json:"pageCount"`
	Reason     string `json:"reason,omitempty"`
}
