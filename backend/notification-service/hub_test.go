package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"BrailleBar/platform"

	"github.com/coder/websocket"
)

func TestHubPushesToTheRightDevice(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dial := func(device string) *websocket.Conn {
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"),
			&websocket.DialOptions{HTTPHeader: http.Header{"X-Device-Id": {device}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.CloseNow() })
		return c
	}
	mine, other := dial("dev-1"), dial("dev-2")
	// ServeWS registers the socket after the handshake, so wait until both are in the hub
	for deadline := time.Now().Add(time.Second); ; time.Sleep(10 * time.Millisecond) {
		hub.mu.Lock()
		n := len(hub.conns)
		hub.mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
	}

	created, _ := platform.NewEnvelope(platform.PrintJobCreatedEvent, platform.PrintJobCreated{
		JobID: "job-1", DocumentID: "doc-1", DeviceID: "dev-1", Pages: [][]string{{"a"}, {"b"}},
	})
	status, _ := platform.NewEnvelope(platform.PrintJobStatusEvent, platform.PrintJobStatus{
		JobID: "job-1", DocumentID: "doc-1", DeviceID: "dev-1", State: "printing", Page: 1, PageCount: 2,
	})
	for _, e := range []platform.Envelope{created, status} {
		if err := hub.Handle(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	for _, want := range []platform.PrintJobStatus{
		{JobID: "job-1", DocumentID: "doc-1", DeviceID: "dev-1", State: "queued", Page: 0, PageCount: 2},
		{JobID: "job-1", DocumentID: "doc-1", DeviceID: "dev-1", State: "printing", Page: 1, PageCount: 2},
	} {
		_, msg, err := mine.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var got platform.PrintJobStatus
		json.Unmarshal(msg, &got)
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}

	short, cancelShort := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancelShort()
	if _, msg, err := other.Read(short); err == nil {
		t.Fatalf("another device received %s", msg)
	}
}

func TestServeWSRejectsMissingDevice(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHub().ServeWS(rec, httptest.NewRequest("GET", "/status", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}
