package config

import (
	"strings"
	"testing"
)

func TestLoad_TelegramConfig(t *testing.T) {
	// Table-driven test — each case exercises a different combination of
	// Telegram env vars. t.Setenv() automatically restores the original
	// value when the subtest ends, so no manual cleanup is needed.
	tests := []struct {
		name        string
		botToken    string
		chatID      string
		webhookBase string // WEBHOOK_BASE_URL
		wantErr     string // substring match; empty means no error expected
		wantConfigured bool
	}{
		{
			name:           "both absent — data-collection-only mode",
			botToken:       "",
			chatID:         "",
			wantConfigured: false,
		},
		{
			name:           "both present with webhook — telegram configured",
			botToken:       "test-token",
			chatID:         "test-chat-id",
			webhookBase:    "https://example.ngrok.io",
			wantConfigured: true,
		},
		{
			name:     "only token set — partial config error",
			botToken: "test-token",
			chatID:   "",
			wantErr:  "must both be set or both be absent",
		},
		{
			name:     "only chat ID set — partial config error",
			botToken: "",
			chatID:   "test-chat-id",
			wantErr:  "must both be set or both be absent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set required env vars so Load() doesn't fail on them.
			t.Setenv("DATABASE_URL", "postgres://test@localhost/test")
			t.Setenv("FPL_LEAGUE_ID", "123")

			// Set Telegram vars — empty string means unset.
			if tt.botToken != "" {
				t.Setenv("TELEGRAM_BOT_TOKEN", tt.botToken)
			} else {
				t.Setenv("TELEGRAM_BOT_TOKEN", "")
			}
			if tt.chatID != "" {
				t.Setenv("TELEGRAM_CHAT_ID", tt.chatID)
			} else {
				t.Setenv("TELEGRAM_CHAT_ID", "")
			}
			if tt.webhookBase != "" {
				t.Setenv("WEBHOOK_BASE_URL", tt.webhookBase)
			} else {
				t.Setenv("WEBHOOK_BASE_URL", "")
			}

			cfg, err := Load()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if cfg.TelegramConfigured != tt.wantConfigured {
				t.Errorf("TelegramConfigured = %v, want %v", cfg.TelegramConfigured, tt.wantConfigured)
			}
		})
	}
}

func TestLoad_WebhookConfig(t *testing.T) {
	// Table-driven test for the new webhook configuration validation.
	// Tests are organized by the rules from the plan:
	//   1. Telegram configured + no WEBHOOK_BASE_URL → error
	//   2. Telegram configured + WEBHOOK_BASE_URL set + no WEBHOOK_SECRET → auto-generates
	//   3. No Telegram → WEBHOOK_BASE_URL is allowed to be absent
	//   4. Telegram configured + explicit WEBHOOK_SECRET → uses it as-is
	tests := []struct {
		name          string
		botToken      string
		chatID        string
		webhookBase   string
		webhookSecret string
		webhookPort   string
		wantErr       string
		checkSecret   bool // if true, verify the secret is populated
		wantPort      int
	}{
		{
			name:        "telegram configured, no WEBHOOK_BASE_URL — error",
			botToken:    "test-token",
			chatID:      "test-chat-id",
			webhookBase: "",
			wantErr:     "WEBHOOK_BASE_URL is required",
		},
		{
			name:        "telegram configured, base URL set, no secret — auto-generates",
			botToken:    "test-token",
			chatID:      "test-chat-id",
			webhookBase: "https://example.ngrok.io",
			checkSecret: true,
			wantPort:    8080,
		},
		{
			name:          "telegram configured, explicit secret — uses it",
			botToken:      "test-token",
			chatID:        "test-chat-id",
			webhookBase:   "https://example.ngrok.io",
			webhookSecret: "my-explicit-secret",
			wantPort:      8080,
		},
		{
			name:        "no telegram — webhook vars not required",
			botToken:    "",
			chatID:      "",
			webhookBase: "",
			wantPort:    8080,
		},
		{
			name:        "custom webhook port",
			botToken:    "test-token",
			chatID:      "test-chat-id",
			webhookBase: "https://example.ngrok.io",
			webhookPort: "9090",
			wantPort:    9090,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://test@localhost/test")
			t.Setenv("FPL_LEAGUE_ID", "123")
			t.Setenv("TELEGRAM_BOT_TOKEN", tt.botToken)
			t.Setenv("TELEGRAM_CHAT_ID", tt.chatID)
			t.Setenv("WEBHOOK_BASE_URL", tt.webhookBase)
			t.Setenv("WEBHOOK_SECRET", tt.webhookSecret)
			if tt.webhookPort != "" {
				t.Setenv("WEBHOOK_PORT", tt.webhookPort)
			} else {
				t.Setenv("WEBHOOK_PORT", "")
			}

			cfg, err := Load()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.checkSecret && cfg.WebhookSecret == "" {
				t.Error("expected auto-generated WebhookSecret, got empty string")
			}
			if tt.checkSecret && len(cfg.WebhookSecret) != 32 {
				// 16 random bytes → 32 hex characters.
				t.Errorf("auto-generated secret length = %d, want 32", len(cfg.WebhookSecret))
			}

			if tt.webhookSecret != "" && cfg.WebhookSecret != tt.webhookSecret {
				t.Errorf("WebhookSecret = %q, want %q", cfg.WebhookSecret, tt.webhookSecret)
			}

			if tt.wantPort != 0 && cfg.WebhookPort != tt.wantPort {
				t.Errorf("WebhookPort = %d, want %d", cfg.WebhookPort, tt.wantPort)
			}
		})
	}
}
