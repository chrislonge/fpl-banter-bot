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

// sendMessage POSTs a single message to the Telegram Bot API.
func (c *Client) sendMessage(ctx context.Context, text string) error {
	body, err := json.Marshal(sendMessageRequest{
		ChatID:    c.chatID,
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
