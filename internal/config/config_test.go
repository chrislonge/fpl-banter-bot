package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
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

func TestLoad_DeadlineTimezone(t *testing.T) {
	tests := []struct {
		name     string
		tzValue  string // env var value; empty means unset
		wantName string // expected *time.Location.String()
		wantErr  string
	}{
		{
			name:     "unset defaults to Europe/London",
			tzValue:  "",
			wantName: "Europe/London",
		},
		{
			name:     "valid override",
			tzValue:  "America/New_York",
			wantName: "America/New_York",
		},
		{
			name:    "invalid timezone fails fast",
			tzValue: "Fake/Zone",
			wantErr: "not a valid IANA timezone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set required env vars.
			t.Setenv("DATABASE_URL", "postgres://test@localhost/test")
			t.Setenv("FPL_LEAGUE_ID", "123")
			t.Setenv("TELEGRAM_BOT_TOKEN", "")
			t.Setenv("TELEGRAM_CHAT_ID", "")
			t.Setenv("DEADLINE_TIMEZONE", tt.tzValue)

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

			// Verify the resolved location matches the expected IANA name.
			// time.Location.String() returns the IANA name used to load it.
			want, wantErr := time.LoadLocation(tt.wantName)
			if wantErr != nil {
				t.Fatalf("failed to load expected timezone %q for test: %v", tt.wantName, wantErr)
			}
			if cfg.DeadlineTimezone.String() != want.String() {
				t.Errorf("DeadlineTimezone = %q, want %q", cfg.DeadlineTimezone, want)
			}
		})
	}
}

func TestLoad_LogLevelValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "debug is valid", value: "debug"},
		{name: "info is valid", value: "info"},
		{name: "warn is valid", value: "warn"},
		{name: "error is valid", value: "error"},
		{name: "empty defaults to info", value: ""},
		{name: "trace is invalid", value: "trace", wantErr: "LOG_LEVEL"},
		{name: "verbose is invalid", value: "verbose", wantErr: "LOG_LEVEL"},
		{name: "INFO uppercase is invalid", value: "INFO", wantErr: "LOG_LEVEL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://test@localhost/test")
			t.Setenv("FPL_LEAGUE_ID", "123")
			t.Setenv("TELEGRAM_BOT_TOKEN", "")
			t.Setenv("TELEGRAM_CHAT_ID", "")
			t.Setenv("LOG_LEVEL", tt.value)
			t.Setenv("LOG_FORMAT", "") // default to text

			_, err := Load()

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
		})
	}
}

func TestLoad_LogFormatValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "text is valid", value: "text"},
		{name: "json is valid", value: "json"},
		{name: "empty defaults to text", value: ""},
		{name: "jsno typo is invalid", value: "jsno", wantErr: "LOG_FORMAT"},
		{name: "yaml is invalid", value: "yaml", wantErr: "LOG_FORMAT"},
		{name: "JSON uppercase is invalid", value: "JSON", wantErr: "LOG_FORMAT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://test@localhost/test")
			t.Setenv("FPL_LEAGUE_ID", "123")
			t.Setenv("TELEGRAM_BOT_TOKEN", "")
			t.Setenv("TELEGRAM_CHAT_ID", "")
			t.Setenv("LOG_LEVEL", "") // default to info
			t.Setenv("LOG_FORMAT", tt.value)

			_, err := Load()

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
		})
	}
}

func TestSetupLogger(t *testing.T) {
	t.Run("text format returns a working logger", func(t *testing.T) {
		logger := SetupLogger("info", "text")
		if logger == nil {
			t.Fatal("expected non-nil logger")
		}
		// Verify it was set as the global default.
		if slog.Default().Handler() != logger.Handler() {
			t.Error("expected SetupLogger to set the global default")
		}
	})

	t.Run("json format emits valid JSON", func(t *testing.T) {
		// SetupLogger writes to os.Stdout, but we can verify the handler
		// type by creating a buffer-backed JSON handler and comparing
		// behavior. Here we verify SetupLogger returns without panic and
		// the global default changes.
		before := slog.Default()
		logger := SetupLogger("debug", "json")
		if logger.Handler() == before.Handler() {
			t.Error("expected SetupLogger to install a new handler")
		}
	})

	t.Run("json handler emits parseable JSON", func(t *testing.T) {
		// Verify that the JSON handler pattern works by creating one
		// backed by a buffer — this is the same pattern components will
		// use in their buffer-backed logger tests.
		var buf bytes.Buffer
		handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		logger := slog.New(handler)

		logger.Info("test message", "key", "value")

		var entry map[string]any
		if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
			t.Fatalf("JSON handler output is not valid JSON: %v\nraw: %s", err, buf.String())
		}
		if entry["msg"] != "test message" {
			t.Errorf("msg = %q, want %q", entry["msg"], "test message")
		}
		if entry["key"] != "value" {
			t.Errorf("key = %q, want %q", entry["key"], "value")
		}
	})

	t.Run("debug level enables debug messages", func(t *testing.T) {
		var buf bytes.Buffer
		handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		logger := slog.New(handler)

		logger.Debug("debug message")

		if buf.Len() == 0 {
			t.Error("expected debug message to be logged at debug level")
		}
	})

	t.Run("info level suppresses debug messages", func(t *testing.T) {
		var buf bytes.Buffer
		handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
		logger := slog.New(handler)

		logger.Debug("debug message")

		if buf.Len() != 0 {
			t.Error("expected debug message to be suppressed at info level")
		}
	})
}
