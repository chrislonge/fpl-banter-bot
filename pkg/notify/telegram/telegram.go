package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

// Compile-time interface check.
//
// Go pattern — COMPILE-TIME INTERFACE SATISFACTION:
//
// This line forces a compile error if *Client ever stops satisfying
// notify.Notifier. The blank identifier _ discards the value; we only
// care about the type assertion. This is a common Go idiom — you'll see
// it in the standard library (e.g., var _ io.Reader = (*os.File)(nil)).
var _ notify.Notifier = (*Client)(nil)

// Client sends formatted alerts to a Telegram chat via the Bot API.
//
// It is safe for concurrent use because it holds no mutable state —
// the underlying *http.Client is also concurrency-safe.
//
// Go pattern — DEPENDENCY INJECTION VIA CONSTRUCTOR:
//
// Like fpl.Client, the caller owns the *http.Client and its timeout
// configuration. In tests, we inject an httptest.Server's client; in
// production, main.go passes an *http.Client with an explicit timeout.
type Client struct {
	httpClient *http.Client
	apiURL     string // e.g., "https://api.telegram.org/bot<token>"
	chatID     string
	logger     *slog.Logger
}

// sendMessageRequest is the JSON body for the Telegram sendMessage API.
type sendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// telegramResponse is the envelope Telegram wraps around every API response.
type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// APIError is returned when the Telegram API responds with an error.
// It preserves the HTTP status code and Telegram's error description
// for debugging — same pattern as fpl.APIError.
type APIError struct {
	StatusCode  int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram API error (HTTP %d): %s", e.StatusCode, e.Description)
}

// New creates a Telegram notifier.
//
//   - httpClient: the HTTP transport (caller owns timeout config).
//   - token: the bot token from @BotFather.
//   - chatID: the target chat (negative for groups).
func New(httpClient *http.Client, token string, chatID string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		httpClient: httpClient,
		apiURL:     "https://api.telegram.org/bot" + token,
		chatID:     chatID,
		logger:     logger,
	}
}

// newWithURL creates a Client with a custom API URL (for tests with httptest).
func newWithURL(httpClient *http.Client, apiURL string, chatID string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		httpClient: httpClient,
		apiURL:     apiURL,
		chatID:     chatID,
		logger:     logger,
	}
}

// ---------------------------------------------------------------------------
// Private JSON-over-HTTP helper
// ---------------------------------------------------------------------------
//
// Go pattern — EXTRACT WHEN DUPLICATION CROSSES THE THRESHOLD:
//
// sendMessageTo, SetWebhook, and DeleteWebhook all repeated the same
// pattern: marshal JSON → POST → read response (capped at 64KB) → check
// HTTP status → parse Telegram envelope → check ok. Adding SetMyCommands
// as a fourth copy was the tipping point for extraction. The helper is
// private because it encodes Telegram-specific response semantics — it's
// not a general-purpose HTTP helper.

// postJSON POSTs a JSON body to a Telegram Bot API method and checks
// the standard response envelope. method is the API method name
// (e.g. "sendMessage", "setWebhook"). payload is marshalled to JSON;
// pass nil for methods with no body (e.g. deleteWebhook).
func (c *Client) postJSON(ctx context.Context, method string, payload any) error {
	var bodyReader io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshalling %s request: %w", method, err)
		}
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/"+method, bodyReader)
	if err != nil {
		return fmt.Errorf("creating %s request: %w", method, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Duration logging with separate paths — resp is nil on network errors.
	// Security: only log method, status_code, duration_ms. Never log apiURL
	// (contains bot token), headers, or request/response bodies.
	start := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(start).Milliseconds()

	// Network error — resp is nil, no status to log.
	if err != nil {
		c.logger.Warn("telegram api error", "method", method, "duration_ms", duration, "error", err)
		return fmt.Errorf("calling %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Cap response reads to prevent unbounded memory allocation from a
	// misbehaving upstream proxy. Telegram responses are typically < 1KB.
	const maxResponseBytes = 1 << 16 // 64KB
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("reading %s response: %w", method, err)
	}

	// Check HTTP status first — a non-200 is always an error, regardless
	// of what the JSON body says. This prevents a pathological case where
	// a non-200 response contains parseable JSON with ok: true.
	if resp.StatusCode != http.StatusOK {
		c.logger.Warn("telegram api non-200", "method", method, "status_code", resp.StatusCode, "duration_ms", duration)
		var tgResp telegramResponse
		if err := json.Unmarshal(respBody, &tgResp); err == nil && tgResp.Description != "" {
			return &APIError{StatusCode: resp.StatusCode, Description: tgResp.Description}
		}
		return &APIError{StatusCode: resp.StatusCode, Description: string(respBody)}
	}

	var tgResp telegramResponse
	if err := json.Unmarshal(respBody, &tgResp); err != nil {
		return fmt.Errorf("parsing %s response: %w", method, err)
	}
	if !tgResp.OK {
		return &APIError{StatusCode: resp.StatusCode, Description: tgResp.Description}
	}

	c.logger.Debug("telegram api call", "method", method, "status_code", resp.StatusCode, "duration_ms", duration)
	return nil
}

// ---------------------------------------------------------------------------
// Message sending
// ---------------------------------------------------------------------------

// SendAlerts formats the alerts and sends each resulting message chunk
// to the configured Telegram chat.
//
// If formatting produces no messages (e.g., empty input), no HTTP calls
// are made and nil is returned.
//
// If any send fails, the error is returned immediately. The poller's
// retry semantics handle transient failures — no retry logic here.
func (c *Client) SendAlerts(ctx context.Context, alerts []notify.Alert) error {
	messages, err := FormatAlerts(alerts)
	if err != nil {
		return fmt.Errorf("formatting alerts: %w", err)
	}

	for _, msg := range messages {
		if err := c.sendMessage(ctx, msg); err != nil {
			return err
		}
	}

	return nil
}

// sendMessage POSTs a single message to the configured chat ID.
//
// Go pattern — DELEGATE TO PARAMETERIZED HELPER:
//
// sendMessage wraps sendMessageTo with the pre-configured chatID. This
// keeps the proactive alert path (SendAlerts → sendMessage) unchanged while
// enabling the reactive command path (SendRaw → sendMessageTo) to target
// an arbitrary chat. In Swift this would be a convenience method with a
// default parameter; Go doesn't have default parameters, so a one-liner
// wrapper is the idiomatic equivalent.
func (c *Client) sendMessage(ctx context.Context, text string) error {
	return c.sendMessageTo(ctx, c.chatID, text)
}

// sendMessageTo POSTs a single HTML-formatted message to an arbitrary chat.
func (c *Client) sendMessageTo(ctx context.Context, chatID, text string) error {
	return c.postJSON(ctx, "sendMessage", sendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
	})
}

// SendRaw sends an HTML-formatted message to an arbitrary chat. This is the
// public entry point used by the bot command handler (via the TelegramBot
// interface) for reactive command replies.
//
// Unlike SendAlerts (which formats structured alerts), SendRaw sends
// pre-formatted text as-is — the bot command layer owns its own formatting.
func (c *Client) SendRaw(ctx context.Context, chatID, text string) error {
	return c.sendMessageTo(ctx, chatID, text)
}

// ---------------------------------------------------------------------------
// Webhook management
// ---------------------------------------------------------------------------

// setWebhookRequest is the JSON body for the Telegram setWebhook API.
type setWebhookRequest struct {
	URL                string `json:"url"`
	DropPendingUpdates bool   `json:"drop_pending_updates"`
}

// SetWebhook registers a webhook URL with Telegram and clears any pending
// updates queued while the bot was offline.
//
// The drop_pending_updates=true flag is critical for operational correctness:
// without it, restarting the bot after a long outage floods the group chat
// with stale command replies. This is called once on startup from RunServer.
func (c *Client) SetWebhook(ctx context.Context, url string) error {
	return c.postJSON(ctx, "setWebhook", setWebhookRequest{
		URL:                url,
		DropPendingUpdates: true,
	})
}

// DeleteWebhook deregisters the webhook on graceful shutdown so Telegram
// stops sending updates to the now-unreachable URL. This is best-effort —
// if it fails, Telegram will eventually time out and stop retrying.
func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.postJSON(ctx, "deleteWebhook", nil)
}

// ---------------------------------------------------------------------------
// Bot command registration
// ---------------------------------------------------------------------------

// BotCommand represents a Telegram bot command for the setMyCommands API.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// BotCommandScope targets command registration to a specific scope.
// Telegram supports several scope types; this bot uses "chat" to register
// commands only in the configured group, preventing them from appearing
// in chats where the bot would silently ignore updates.
//
// Pass nil for the default scope (global, all users/chats; distinct from "all_private_chats").
type BotCommandScope struct {
	Type   string `json:"type"`
	ChatID int64  `json:"chat_id,omitempty"`
}

type setMyCommandsRequest struct {
	Commands []BotCommand     `json:"commands"`
	Scope    *BotCommandScope `json:"scope,omitempty"`
}

type deleteMyCommandsRequest struct {
	Scope *BotCommandScope `json:"scope,omitempty"`
}

// SetMyCommands registers the bot's command list with Telegram so users
// see autocomplete suggestions when typing "/". Commands are registered
// for the given scope — use BotCommandScope{Type: "chat", ChatID: id}
// to target a specific chat.
//
// This is idempotent: calling it on every startup replaces the previous
// list, ensuring the menu always matches the running code.
func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand, scope *BotCommandScope) error {
	return c.postJSON(ctx, "setMyCommands", setMyCommandsRequest{
		Commands: commands,
		Scope:    scope,
	})
}

// DeleteMyCommands removes the bot's command list for the given scope.
// Pass nil for the default scope. This is used on startup to clear any
// previously-set global commands that might leak into chats where the
// bot doesn't respond.
func (c *Client) DeleteMyCommands(ctx context.Context, scope *BotCommandScope) error {
	return c.postJSON(ctx, "deleteMyCommands", deleteMyCommandsRequest{
		Scope: scope,
	})
}
