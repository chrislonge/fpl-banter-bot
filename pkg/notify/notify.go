// Package notify defines the Notifier interface for sending alerts to chat
// platforms. Platform-specific implementations (Telegram, Discord, etc.)
// live in subdirectories.
//
// This package is in pkg/ (not internal/) because it is the public extension
// point — anyone building a new chat adapter imports this interface.
package notify

import "context"

// Alert represents a single banter-worthy event detected by the stats engine.
// The stats engine produces these; the Notifier consumes them.
type Alert struct {
	// Type categorises the alert (e.g., "rank_change", "streak", "chip_usage").
	Type string

	// Message is the human-readable text to send.
	Message string
}

// Notifier sends alerts to a chat platform. Implementations must be safe for
// concurrent use (the poller may call from a goroutine).
//
// In Go, interfaces are satisfied implicitly — a struct just needs to have
// the right method signatures. No "implements" keyword required.
type Notifier interface {
	// SendAlerts delivers one or more alerts to the configured chat.
	// context.Context is the first parameter by convention — it carries
	// deadlines, cancellation signals, and request-scoped values. Any
	// function that does I/O (network, disk, DB) should accept a context.
	SendAlerts(ctx context.Context, alerts []Alert) error
}
