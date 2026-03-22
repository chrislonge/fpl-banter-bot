package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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
func New(httpClient *http.Client, token string, chatID string) *Client {
	return &Client{
		httpClient: httpClient,
		apiURL:     "https://api.telegram.org/bot" + token,
		chatID:     chatID,
	}
}

// newWithURL creates a Client with a custom API URL (for tests with httptest).
func newWithURL(httpClient *http.Client, apiURL string, chatID string) *Client {
	return &Client{
		httpClient: httpClient,
		apiURL:     apiURL,
		chatID:     chatID,
	}
}

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
	body, err := json.Marshal(sendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
	})
	if err != nil {
		return fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending message: %w", err)
	}
	defer resp.Body.Close()

	// Cap response reads to prevent unbounded memory allocation from a
	// misbehaving upstream proxy. Telegram responses are typically < 1KB.
	const maxResponseBytes = 1 << 16 // 64KB
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	// Check HTTP status first — a non-200 is always an error, regardless
	// of what the JSON body says. This prevents a pathological case where
	// a non-200 response contains parseable JSON with ok: true.
	if resp.StatusCode != http.StatusOK {
		var tgResp telegramResponse
		if err := json.Unmarshal(respBody, &tgResp); err == nil && tgResp.Description != "" {
			return &APIError{
				StatusCode:  resp.StatusCode,
				Description: tgResp.Description,
			}
		}
		return &APIError{
			StatusCode:  resp.StatusCode,
			Description: string(respBody),
		}
	}

	// Parse the Telegram response envelope for 200 responses.
	var tgResp telegramResponse
	if err := json.Unmarshal(respBody, &tgResp); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	if !tgResp.OK {
		return &APIError{
			StatusCode:  resp.StatusCode,
			Description: tgResp.Description,
		}
	}

	return nil
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
	body, err := json.Marshal(setWebhookRequest{
		URL:                url,
		DropPendingUpdates: true,
	})
	if err != nil {
		return fmt.Errorf("marshalling setWebhook request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/setWebhook", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating setWebhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling setWebhook: %w", err)
	}
	defer resp.Body.Close()

	const maxResponseBytes = 1 << 16
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("reading setWebhook response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var tgResp telegramResponse
		if err := json.Unmarshal(respBody, &tgResp); err == nil && tgResp.Description != "" {
			return &APIError{StatusCode: resp.StatusCode, Description: tgResp.Description}
		}
		return &APIError{StatusCode: resp.StatusCode, Description: string(respBody)}
	}

	var tgResp telegramResponse
	if err := json.Unmarshal(respBody, &tgResp); err != nil {
		return fmt.Errorf("parsing setWebhook response: %w", err)
	}
	if !tgResp.OK {
		return &APIError{StatusCode: resp.StatusCode, Description: tgResp.Description}
	}

	return nil
}

// DeleteWebhook deregisters the webhook on graceful shutdown so Telegram
// stops sending updates to the now-unreachable URL. This is best-effort —
// if it fails, Telegram will eventually time out and stop retrying.
func (c *Client) DeleteWebhook(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/deleteWebhook", nil)
	if err != nil {
		return fmt.Errorf("creating deleteWebhook request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling deleteWebhook: %w", err)
	}
	defer resp.Body.Close()

	const maxResponseBytes = 1 << 16
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("reading deleteWebhook response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var tgResp telegramResponse
		if err := json.Unmarshal(respBody, &tgResp); err == nil && tgResp.Description != "" {
			return &APIError{StatusCode: resp.StatusCode, Description: tgResp.Description}
		}
		return &APIError{StatusCode: resp.StatusCode, Description: string(respBody)}
	}

	var tgResp telegramResponse
	if err := json.Unmarshal(respBody, &tgResp); err != nil {
		return fmt.Errorf("parsing deleteWebhook response: %w", err)
	}
	if !tgResp.OK {
		return &APIError{StatusCode: resp.StatusCode, Description: tgResp.Description}
	}

	return nil
}
