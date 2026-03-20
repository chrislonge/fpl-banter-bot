// Package config loads and validates application configuration from
// environment variables. All configuration is external — nothing is
// hardcoded — following the twelve-factor app methodology.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all application configuration. Fields are populated from
// environment variables in Load(). Required fields cause Load() to return
// an error if missing; optional fields have defaults.
type Config struct {
	// FPL settings
	FPLLeagueID   int
	FPLLeagueType string

	// Telegram settings (optional — omit both for data-collection-only mode)
	TelegramBotToken   string
	TelegramChatID     string
	TelegramConfigured bool // true when both Telegram vars are present

	// Database
	DatabaseURL string

	// Polling intervals (seconds)
	PollIdleInterval       int
	PollLiveInterval       int
	PollProcessingInterval int

	// Logging
	LogLevel string
}

// Load reads environment variables and returns a validated Config.
// It returns an error if any required variable is missing or invalid.
func Load() (Config, error) {
	// required reads a required env var and returns an error if empty.
	// This is a closure — a function defined inside another function that
	// captures variables from its enclosing scope. Here it's a convenient
	// way to avoid repeating the "check empty, return error" pattern.
	required := func(key string) (string, error) {
		v := os.Getenv(key)
		if v == "" {
			return "", fmt.Errorf("required environment variable %s is not set", key)
		}
		return v, nil
	}

	dbURL, err := required("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}

	leagueIDStr, err := required("FPL_LEAGUE_ID")
	if err != nil {
		return Config{}, err
	}

	// strconv.Atoi converts a string to an int. It returns an error if
	// the string isn't a valid integer (e.g., "abc" or "12.5").
	leagueID, err := strconv.Atoi(leagueIDStr)
	if err != nil {
		return Config{}, fmt.Errorf("FPL_LEAGUE_ID must be an integer: %w", err)
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")

	// Partial Telegram config is a misconfiguration — fail fast.
	// Both present = okay, both absent = data-collection-only, one missing = error.
	if (botToken == "") != (chatID == "") {
		return Config{}, fmt.Errorf(
			"TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID must both be set or both be absent",
		)
	}

	return Config{
		FPLLeagueID:            leagueID,
		FPLLeagueType:          getEnvOrDefault("FPL_LEAGUE_TYPE", "h2h"),
		TelegramBotToken:       botToken,
		TelegramChatID:         chatID,
		TelegramConfigured:     botToken != "" && chatID != "",
		DatabaseURL:            dbURL,
		PollIdleInterval:       getEnvAsIntOrDefault("POLL_IDLE_INTERVAL", 21600),
		PollLiveInterval:       getEnvAsIntOrDefault("POLL_LIVE_INTERVAL", 900),
		PollProcessingInterval: getEnvAsIntOrDefault("POLL_PROCESSING_INTERVAL", 600),
		LogLevel:               getEnvOrDefault("LOG_LEVEL", "info"),
	}, nil
}

// getEnvOrDefault returns the value of an env var, or a default if unset.
func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// getEnvAsIntOrDefault returns an env var parsed as int, or a default.
// If the var is set but not a valid integer, the default is returned.
func getEnvAsIntOrDefault(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return i
}
