package bot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestServeWebhook_ValidUpdate verifies that a valid POST to the webhook
// path returns 200 and eventually dispatches the command.
//
// Go pattern — TESTING ASYNC GOROUTINES:
//
// serveWebhook dispatches handleUpdate in a background goroutine (the
// ack-then-async pattern). We can't assert on the goroutine's effects
// synchronously — we need to give it time to complete. A short time.Sleep
// is acceptable in tests (not production code) for this pattern. An
// alternative would be injecting a channel-based callback, but that would
// add complexity to the production code just to support tests.
func TestServeWebhook_ValidUpdate(t *testing.T) {
	tg := &fakeTelegramBot{}
	sq := &fakeStatsQuerier{streaks: nil} // /streak returns "No streaks yet"
	h := newTestHandler(tg, sq, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	// Set up a mux with the webhook path (same as RunServer does).
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/"+h.webhookSecret, h.serveWebhook)

	server := httptest.NewServer(mux)
	defer server.Close()

	// Build a valid Telegram update JSON.
	upd := update{
		UpdateID: 42,
		Message: &message{
			MessageID: 1,
			Text:      "/streak",
			Chat:      chat{ID: -12345},
		},
	}
	body, _ := json.Marshal(upd)

	resp, err := http.Post(server.URL+"/webhook/"+h.webhookSecret, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// The webhook should ack 200 immediately.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Poll for the background goroutine to complete instead of using a
	// fixed sleep. This is more reliable than time.Sleep(100ms) which
	// can flake on slow CI runners.
	deadline := time.Now().Add(2 * time.Second)
	var sent []sentMessage
	for time.Now().Before(deadline) {
		sent = tg.sentMessages()
		if len(sent) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sent))
	}
	if !strings.Contains(sent[0].text, "No streaks yet") {
		t.Errorf("response = %q, want 'No streaks yet'", sent[0].text)
	}
}

// TestServeWebhook_WrongPath verifies that a request to a path with the
// wrong secret gets a 404 from the mux (not the handler).
func TestServeWebhook_WrongPath(t *testing.T) {
	tg := &fakeTelegramBot{}
	h := newTestHandler(tg, &fakeStatsQuerier{}, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/"+h.webhookSecret, h.serveWebhook)

	server := httptest.NewServer(mux)
	defer server.Close()

	upd := update{
		UpdateID: 42,
		Message: &message{
			MessageID: 1,
			Text:      "/streak",
			Chat:      chat{ID: -12345},
		},
	}
	body, _ := json.Marshal(upd)

	resp, err := http.Post(server.URL+"/webhook/wrong-secret", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestServeWebhook_BadJSON verifies that malformed JSON returns 400.
func TestServeWebhook_BadJSON(t *testing.T) {
	tg := &fakeTelegramBot{}
	h := newTestHandler(tg, &fakeStatsQuerier{}, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/"+h.webhookSecret, h.serveWebhook)

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL+"/webhook/"+h.webhookSecret, "application/json",
		bytes.NewReader([]byte("not valid json")))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestServeWebhook_MethodNotAllowed verifies that GET returns 405.
func TestServeWebhook_MethodNotAllowed(t *testing.T) {
	tg := &fakeTelegramBot{}
	h := newTestHandler(tg, &fakeStatsQuerier{}, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	req := httptest.NewRequest(http.MethodGet, "/webhook/"+h.webhookSecret, nil)
	rec := httptest.NewRecorder()

	h.serveWebhook(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
