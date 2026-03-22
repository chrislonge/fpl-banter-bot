package bot

import (
	"context"
	"encoding/json"
	"net/http"
)

// serveWebhook handles POST /webhook/<secret>.
//
// It acks 200 immediately (before processing) to prevent Telegram retries
// on slow commands like /deadline that make outbound FPL API calls.
//
// Go pattern — ACK-THEN-ASYNC:
//
// HTTP handlers in Go run in their own goroutine already (net/http spawns
// one per request). Spawning a second goroutine here detaches command
// processing from the HTTP response lifecycle. We use context.Background()
// (not r.Context()) because r.Context() cancels when the HTTP response is
// sent — which happens at the end of this function, immediately after the
// 200 ack. If we passed r.Context() to handleUpdate, any outbound HTTP
// call (like /deadline fetching bootstrap data) would get cancelled the
// moment we ack.
//
// In Swift, this is like calling Task.detached { ... } inside a URLSession
// delegate — the response goes back to the caller immediately while the
// background work continues independently.
//
// Telegram retries are rare with a correct ack; if a retry does slip
// through, replying twice to a chat command is acceptable and harmless.
func (h *Handler) serveWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var upd update
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Ack before any processing — Telegram expects a response within 5s.
	w.WriteHeader(http.StatusOK)

	// Dispatch in a detached goroutine with a fresh context.
	go h.handleUpdate(context.Background(), upd)
}
