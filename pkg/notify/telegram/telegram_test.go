package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// testServer creates an httptest server that records requests and returns
// canned responses. The caller controls the response via statusCode and body.
func testServer(t *testing.T, statusCode int, body string) (*httptest.Server, *[]sendMessageRequest) {
	t.Helper()

	var mu sync.Mutex
	var received []sendMessageRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var req sendMessageRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			t.Errorf("unmarshalling request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		mu.Lock()
		received = append(received, req)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write([]byte(body))
	}))

	t.Cleanup(server.Close)
	return server, &received
}

// rawRequest records the HTTP method, URL path, and raw JSON body.
type rawRequest struct {
	Method string
	Path   string
	Body   json.RawMessage
}

// rawTestServer creates an httptest server that captures raw request bodies
// (not tied to a specific request type). Used for testing SetMyCommands,
// DeleteMyCommands, and other non-sendMessage API calls.
func rawTestServer(t *testing.T, statusCode int, body string) (*httptest.Server, *[]rawRequest) {
	t.Helper()

	var mu sync.Mutex
	var received []rawRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		mu.Lock()
		received = append(received, rawRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   json.RawMessage(bodyBytes),
		})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write([]byte(body))
	}))

	t.Cleanup(server.Close)
	return server, &received
}

// singleAlert returns a minimal alert slice for testing the HTTP layer.
func singleAlert() []notify.Alert {
	return []notify.Alert{
		{
			Kind:     notify.AlertKindH2HResult,
			LeagueID: 100,
			EventID:  10,
			H2HResult: &notify.H2HResultAlert{
				Manager1: notify.ManagerRef{ID: 1, Name: "Chris", TeamName: "Chris FC"},
				Score1:   65,
				Manager2: notify.ManagerRef{ID: 2, Name: "Dave", TeamName: "Dave FC"},
				Score2:   42,
				WinnerID: int64Ptr(1),
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSendAlerts_Success(t *testing.T) {
	server, received := testServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	err := client.SendAlerts(context.Background(), singleAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*received) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*received))
	}

	req := (*received)[0]

	// Verify chat_id matches configured value.
	if req.ChatID != "-12345" {
		t.Errorf("chat_id = %q, want %q", req.ChatID, "-12345")
	}

	// Verify parse_mode is always HTML.
	if req.ParseMode != "HTML" {
		t.Errorf("parse_mode = %q, want %q", req.ParseMode, "HTML")
	}

	// Verify message text is non-empty.
	if req.Text == "" {
		t.Error("message text is empty")
	}
}

func TestSendAlerts_MultipleChunks(t *testing.T) {
	server, received := testServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	// Generate enough alerts to force multiple chunks.
	var alerts []notify.Alert
	for i := 0; i < 120; i++ {
		m1 := notify.ManagerRef{ID: int64(i*2 + 1), Name: "VeryLongPlayerNameForChunking" + string(rune('A'+i%26)), TeamName: "FC"}
		m2 := notify.ManagerRef{ID: int64(i*2 + 2), Name: "VeryLongPlayerNameForChunking" + string(rune('a'+i%26)), TeamName: "FC"}
		alerts = append(alerts, notify.Alert{
			Kind:     notify.AlertKindH2HResult,
			LeagueID: 100,
			EventID:  10,
			H2HResult: &notify.H2HResultAlert{
				Manager1: m1, Score1: 50 + i,
				Manager2: m2, Score2: 40 + i,
				WinnerID: int64Ptr(m1.ID),
			},
		})
	}

	err := client.SendAlerts(context.Background(), alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*received) < 2 {
		t.Fatalf("expected multiple requests for chunked messages, got %d", len(*received))
	}

	// Every request should have the correct chat_id and parse_mode.
	for i, req := range *received {
		if req.ChatID != "-12345" {
			t.Errorf("request %d: chat_id = %q, want %q", i, req.ChatID, "-12345")
		}
		if req.ParseMode != "HTML" {
			t.Errorf("request %d: parse_mode = %q, want %q", i, req.ParseMode, "HTML")
		}
	}
}

func TestSendAlerts_Non200Status(t *testing.T) {
	server, _ := testServer(t, http.StatusInternalServerError,
		`{"ok": false, "description": "Internal Server Error"}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	err := client.SendAlerts(context.Background(), singleAlert())
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", apiErr.StatusCode, http.StatusInternalServerError)
	}
}

func TestSendAlerts_TelegramOKFalse(t *testing.T) {
	server, _ := testServer(t, http.StatusBadRequest,
		`{"ok": false, "description": "Bad Request: chat not found"}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	err := client.SendAlerts(context.Background(), singleAlert())
	if err == nil {
		t.Fatal("expected error when ok=false")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Description != "Bad Request: chat not found" {
		t.Errorf("description = %q, want %q", apiErr.Description, "Bad Request: chat not found")
	}
}

func TestSendAlerts_RequestBodyContainsChatID(t *testing.T) {
	server, received := testServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-9876543210")

	err := client.SendAlerts(context.Background(), singleAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*received) == 0 {
		t.Fatal("no requests received")
	}
	if (*received)[0].ChatID != "-9876543210" {
		t.Errorf("chat_id = %q, want %q", (*received)[0].ChatID, "-9876543210")
	}
}

func TestSendAlerts_ParseModeIsHTML(t *testing.T) {
	server, received := testServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	err := client.SendAlerts(context.Background(), singleAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*received) == 0 {
		t.Fatal("no requests received")
	}
	if (*received)[0].ParseMode != "HTML" {
		t.Errorf("parse_mode = %q, want %q", (*received)[0].ParseMode, "HTML")
	}
}

func TestSendAlerts_EmptyAlerts(t *testing.T) {
	server, received := testServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	err := client.SendAlerts(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*received) != 0 {
		t.Errorf("expected no HTTP calls for empty alerts, got %d", len(*received))
	}
}

func TestSendAlerts_ContextCancellation(t *testing.T) {
	// Create a server that always succeeds — but we'll cancel the context
	// before the request is sent.
	server, _ := testServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := client.SendAlerts(ctx, singleAlert())
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SetMyCommands / DeleteMyCommands tests
// ---------------------------------------------------------------------------

func TestSetMyCommands_Success(t *testing.T) {
	server, received := rawTestServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	commands := []BotCommand{
		{Command: "standings", Description: "Current league standings"},
		{Command: "streak", Description: "Active streaks"},
	}
	scope := &BotCommandScope{Type: "chat", ChatID: -12345}

	err := client.SetMyCommands(context.Background(), commands, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*received) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*received))
	}

	req := (*received)[0]
	if req.Path != "/setMyCommands" {
		t.Errorf("path = %q, want /setMyCommands", req.Path)
	}

	// Verify the request body contains commands and scope.
	var body setMyCommandsRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("unmarshalling request body: %v", err)
	}
	if len(body.Commands) != 2 {
		t.Errorf("commands count = %d, want 2", len(body.Commands))
	}
	if body.Commands[0].Command != "standings" {
		t.Errorf("first command = %q, want %q", body.Commands[0].Command, "standings")
	}
	if body.Scope == nil {
		t.Fatal("scope is nil, want chat scope")
	}
	if body.Scope.Type != "chat" {
		t.Errorf("scope type = %q, want %q", body.Scope.Type, "chat")
	}
	if body.Scope.ChatID != -12345 {
		t.Errorf("scope chat_id = %d, want %d", body.Scope.ChatID, -12345)
	}
}

func TestSetMyCommands_NilScope(t *testing.T) {
	server, received := rawTestServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	commands := []BotCommand{
		{Command: "standings", Description: "Current league standings"},
	}

	err := client.SetMyCommands(context.Background(), commands, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*received) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*received))
	}

	// Verify scope is omitted from JSON when nil.
	var body map[string]json.RawMessage
	if err := json.Unmarshal((*received)[0].Body, &body); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	if _, ok := body["scope"]; ok {
		t.Error("scope should be omitted when nil")
	}
}

func TestSetMyCommands_APIError(t *testing.T) {
	server, _ := rawTestServer(t, http.StatusBadRequest,
		`{"ok": false, "description": "Bad Request: invalid command"}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	err := client.SetMyCommands(context.Background(), []BotCommand{
		{Command: "standings", Description: "test"},
	}, nil)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", apiErr.StatusCode, http.StatusBadRequest)
	}
}

func TestDeleteMyCommands_Success(t *testing.T) {
	server, received := rawTestServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	// nil scope targets the default scope.
	err := client.DeleteMyCommands(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*received) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*received))
	}
	if (*received)[0].Path != "/deleteMyCommands" {
		t.Errorf("path = %q, want /deleteMyCommands", (*received)[0].Path)
	}
}

func TestDeleteMyCommands_WithChatScope(t *testing.T) {
	server, received := rawTestServer(t, http.StatusOK, `{"ok": true}`)
	client := newWithURL(server.Client(), server.URL, "-12345")

	scope := &BotCommandScope{Type: "chat", ChatID: -12345}
	err := client.DeleteMyCommands(context.Background(), scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*received) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*received))
	}

	var body deleteMyCommandsRequest
	if err := json.Unmarshal((*received)[0].Body, &body); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	if body.Scope == nil || body.Scope.Type != "chat" {
		t.Errorf("scope = %+v, want chat scope", body.Scope)
	}
}
