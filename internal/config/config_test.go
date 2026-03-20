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
			name:           "both present — telegram configured",
			botToken:       "test-token",
			chatID:         "test-chat-id",
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
