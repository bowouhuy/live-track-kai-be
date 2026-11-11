// Package realtime fans out position updates to browsers over Server-Sent Events.
package realtime

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// EventName is the SSE event carrying {"trains":[...]}.
const EventName = "positions.updated"

// Hub keeps the latest payload and the set of connected clients.
type Hub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	last    []byte
}

func NewHub() *Hub { return &Hub{clients: make(map[chan []byte]struct{})} }

// Publish stores the payload and sends it to every client. Slow clients that
// still have an unread payload simply skip this one (they get the next tick).
func (h *Hub) Publish(payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = payload
	for ch := range h.clients {
		select {
		case ch <- payload:
		default:
		}
	}
}

// Clients returns the number of connected clients.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *Hub) subscribe() (chan []byte, []byte) {
	ch := make(chan []byte, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[ch] = struct{}{}
	return ch, h.last
}

func (h *Hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, ch)
}

// ServeHTTP streams events. The latest payload is sent immediately on connect,
// so clients need no separate initial fetch (and no race with the stream).
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // long-lived response

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache, no-transform")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no") // tell nginx not to buffer
	w.WriteHeader(http.StatusOK)

	ch, last := h.subscribe()
	defer h.unsubscribe(ch)

	// Ask the browser to wait 3s before reconnecting after a drop.
	if _, err := fmt.Fprint(w, "retry: 3000\n\n"); err != nil {
		return
	}
	if last != nil {
		if err := writeEvent(w, last); err != nil {
			return
		}
	}
	if err := rc.Flush(); err != nil {
		return
	}

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case payload := <-ch:
			if err := writeEvent(w, payload); err != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, payload []byte) error {
	if _, err := fmt.Fprintf(w, "event: %s\ndata: ", EventName); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, "\n\n")
	return err
}

// RunBroadcaster calls compute every interval and publishes the result. A
// failing or panicking tick is logged and skipped; the loop keeps running.
func RunBroadcaster(ctx context.Context, h *Hub, interval time.Duration, compute func() ([]byte, error)) {
	tick := func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("broadcast tick panicked", "panic", rec)
			}
		}()
		payload, err := compute()
		if err != nil {
			slog.Error("broadcast tick failed", "err", err)
			return
		}
		h.Publish(payload)
	}

	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
