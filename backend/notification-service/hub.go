package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"BrailleBar/platform"

	"github.com/coder/websocket"
)

// Hub keeps the open status sockets per device and pushes job updates to them.
type Hub struct {
	mu    sync.Mutex
	conns map[string]map[chan []byte]struct{}
}

func NewHub() *Hub { return &Hub{conns: map[string]map[chan []byte]struct{}{}} }

// ServeWS is WS /status. The gateway has already put the device id from ?deviceId= in X-Device-Id.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	deviceID := r.Header.Get("X-Device-Id")
	if deviceID == "" || len(deviceID) > 64 {
		http.Error(w, "missing or invalid device id", http.StatusBadRequest)
		return
	}
	// No cookies are involved and the device id is the only credential, so the Origin check adds nothing;
	// it would only block the Tauri webview, whose origin differs from the gateway's.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return // Accept already answered
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context()) // the app never sends; this ends ctx when it disconnects

	ch := make(chan []byte, 16)
	h.add(deviceID, ch)
	defer h.remove(deviceID, ch)
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// Handle turns print.job.created and print.job.status into one PrintJobStatus message for the device.
// Never an error: a push nobody receives is not worth a retry, the app can GET /jobs/{id}.
func (h *Hub) Handle(ctx context.Context, e platform.Envelope) error {
	var s platform.PrintJobStatus
	switch e.Type {
	case platform.PrintJobCreatedEvent:
		var p platform.PrintJobCreated
		if err := e.Decode(&p); err != nil {
			return err
		}
		s = platform.PrintJobStatus{JobID: p.JobID, DocumentID: p.DocumentID, DeviceID: p.DeviceID, State: "queued", PageCount: len(p.Pages)}
	case platform.PrintJobStatusEvent:
		if err := e.Decode(&s); err != nil {
			return err
		}
	default:
		return nil
	}
	msg, err := json.Marshal(s)
	if err != nil {
		return err
	}
	h.send(s.DeviceID, msg)
	return nil
}

func (h *Hub) send(deviceID string, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.conns[deviceID] {
		select {
		case ch <- msg:
		default: // ponytail: a socket 16 messages behind loses this one; fine while the app re-reads GET /jobs/{id} on reconnect
		}
	}
}

func (h *Hub) add(deviceID string, ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[deviceID] == nil {
		h.conns[deviceID] = map[chan []byte]struct{}{}
	}
	h.conns[deviceID][ch] = struct{}{}
}

func (h *Hub) remove(deviceID string, ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns[deviceID], ch)
	if len(h.conns[deviceID]) == 0 {
		delete(h.conns, deviceID)
	}
}
