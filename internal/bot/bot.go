package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/stats"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
)

// ---------------------------------------------------------------------------
// Consumer-defined interfaces
// ---------------------------------------------------------------------------
//
// Go pattern — CONSUMER-DEFINED INTERFACE (Interface Segregation Principle):
//
// Each interface below declares only the methods that the bot handler
// actually calls. The concrete types (*telegram.Client, *stats.Engine,
// *store.PostgresStore, *fpl.Client, *poller.Poller) satisfy these
// interfaces implicitly via Go's structural typing — no cast or
// "implements" keyword needed.
//
// This keeps the bot package decoupled from every dependency's full API
// surface. Tests only need to fake 1-2 methods per interface instead of
// the full concrete type.
//
// In Swift, these would be protocols; in Kotlin, interfaces. The key
// difference in Go: the consumer defines them, not the producer.
// ---------------------------------------------------------------------------

// TelegramBot defines the subset of telegram.Client the handler needs.
type TelegramBot interface {
	SendRaw(ctx context.Context, chatID, text string) error
	SetWebhook(ctx context.Context, url string) error
	DeleteWebhook(ctx context.Context) error
}

// StatsQuerier defines the read-side stats dependency.
type StatsQuerier interface {
	GetCurrentStreaks(ctx context.Context) ([]stats.CurrentStreak, error)
	GetH2HRecord(ctx context.Context, managerAID, managerBID int64) (stats.H2HRecord, error)
}

// LeagueStore defines the read-side store dependency.
type LeagueStore interface {
	GetStandings(ctx context.Context, leagueID int64, eventID int) ([]store.GameweekStanding, error)
	GetManagers(ctx context.Context, leagueID int64) ([]store.Manager, error)
	GetLatestEventID(ctx context.Context, leagueID int64) (int, error)
}

// FPLQuerier defines the bootstrap data dependency for /deadline.
type FPLQuerier interface {
	GetBootstrap(ctx context.Context) (fpl.BootstrapResponse, error)
}

// PollerStatusProvider exposes only what the health endpoint needs.
// Defining a narrow interface here means bot_test.go doesn't need to
// import the full poller package — a simple fake with one method suffices.
type PollerStatusProvider interface {
	Status() (state string, lastProcessedEvent int)
}

// ---------------------------------------------------------------------------
// Telegram update types
// ---------------------------------------------------------------------------
//
// These are minimal representations of Telegram's Update object — we only
// decode the fields we need for command routing. The full Update type has
// dozens of fields for inline queries, callbacks, etc. that we don't use.

type update struct {
	UpdateID int      `json:"update_id"`
	Message  *message `json:"message"`
}

type message struct {
	MessageID int    `json:"message_id"`
	Text      string `json:"text"`
	Chat      chat   `json:"chat"`
}

type chat struct {
	ID int64 `json:"id"`
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

// Config holds the bot server's configuration. Passed to New() to keep
// the constructor signature clean when there are many parameters.
//
// Go pattern — OPTIONS STRUCT:
//
// When a constructor needs more than 3-4 parameters, a config struct is
// clearer than a long parameter list. It also makes adding new options
// backward-compatible — you add a field with a zero-value default. In
// Swift this is the same idea as a Configuration struct with memberwise init.
type Config struct {
	LeagueID       int64
	ChatID         string
	Port           int
	WebhookBaseURL string
	WebhookSecret  string
}

// Handler is the core bot server. It routes incoming Telegram updates to
// command handlers and serves a health endpoint.
type Handler struct {
	tg     TelegramBot
	stats  StatsQuerier
	store  LeagueStore
	fpl    FPLQuerier
	poller PollerStatusProvider

	leagueID      int64
	chatID        string
	port          int
	webhookSecret string
	webhookURL    string // full registered URL: base + /webhook/ + secret
}

// New creates a Handler with all its dependencies.
//
// The concrete types are injected as interfaces — main.go wires the real
// implementations, while tests wire fakes. This is the Dependency Inversion
// Principle in action: the bot defines what it needs (interfaces), and the
// caller provides implementations.
func New(
	tg TelegramBot,
	statsQuerier StatsQuerier,
	leagueStore LeagueStore,
	fplQuerier FPLQuerier,
	poller PollerStatusProvider,
	cfg Config,
) *Handler {
	webhookURL := strings.TrimRight(cfg.WebhookBaseURL, "/") + "/webhook/" + cfg.WebhookSecret
	return &Handler{
		tg:            tg,
		stats:         statsQuerier,
		store:         leagueStore,
		fpl:           fplQuerier,
		poller:        poller,
		leagueID:      cfg.LeagueID,
		chatID:        cfg.ChatID,
		port:          cfg.Port,
		webhookSecret: cfg.WebhookSecret,
		webhookURL:    webhookURL,
	}
}

// RunServer starts the HTTP server that handles webhooks and health checks.
// It blocks until the context is cancelled, then gracefully shuts down.
//
// Go pattern — CANCELLABLE HTTP SERVER:
//
// http.ListenAndServe blocks forever, so we run it in a goroutine and use
// a select to race between "context cancelled" and "server error." When
// the context cancels (e.g., SIGTERM), we call srv.Shutdown for a graceful
// drain of in-flight requests, then deregister the webhook.
//
// This is the canonical Go recipe for a server with graceful shutdown.
// In Swift/Kotlin, you'd use structured concurrency (TaskGroup / coroutine
// scope) with cancellation — the semantics are identical, just the syntax
// differs.
func (h *Handler) RunServer(ctx context.Context) error {
	// Register the webhook with Telegram on startup.
	if err := h.tg.SetWebhook(ctx, h.webhookURL); err != nil {
		return fmt.Errorf("registering webhook %s: %w", h.webhookURL, err)
	}
	slog.Info("webhook registered", "url", h.webhookURL)

	// Go pattern — PATH-BASED SECRET:
	//
	// The webhook path includes the secret token: /webhook/<secret>.
	// Any request to a different path gets an automatic 404 from the mux.
	// This is simpler than the X-Telegram-Bot-Api-Secret-Token header
	// approach and is what PROJECT_PLAN.md specifies.
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/"+h.webhookSecret, h.serveWebhook)
	mux.HandleFunc("/health", h.serveHealth)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", h.port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start the HTTP server in a background goroutine. We can't just call
	// srv.ListenAndServe() directly because it blocks — we need to be able
	// to react to context cancellation in the select below.
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	slog.Info("bot server listening", "port", h.port)

	select {
	case <-ctx.Done():
		// Graceful shutdown: drain in-flight requests, then deregister webhook.
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Best-effort webhook deregistration — if it fails, Telegram will
		// eventually time out and stop retrying on its own.
		if err := h.tg.DeleteWebhook(shutCtx); err != nil {
			slog.Warn("deleteWebhook failed during shutdown", "error", err)
		} else {
			slog.Info("webhook deregistered")
		}

		return srv.Shutdown(shutCtx)

	case err := <-errCh:
		// The server exited unexpectedly. ErrServerClosed is normal after
		// Shutdown(), but any other error is a real problem.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("webhook server: %w", err)
	}
}

// serveHealth returns a JSON health check including poller state.
// This endpoint is always available regardless of webhook registration.
func (h *Handler) serveHealth(w http.ResponseWriter, _ *http.Request) {
	state, lastEvent := h.poller.Status()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","poller_state":%q,"last_processed_event":%d}`,
		state, lastEvent)
}

// handleUpdate validates the incoming Telegram update and dispatches the
// command. It is called from a detached goroutine (see serveWebhook) with
// context.Background().
//
// Security: only updates from the configured chat ID are processed. This
// prevents other Telegram users/groups from invoking commands if they
// somehow discover the webhook URL.
func (h *Handler) handleUpdate(ctx context.Context, upd update) {
	if upd.Message == nil {
		return
	}

	// Chat ID filtering — only respond to the configured group chat.
	if strconv.FormatInt(upd.Message.Chat.ID, 10) != h.chatID {
		slog.Debug("ignoring update from unauthorized chat",
			"chat_id", upd.Message.Chat.ID)
		return
	}

	// Parse the command from the message text.
	// Telegram commands look like "/standings" or "/standings@BotName".
	// We strip the @BotName suffix so the dispatch logic doesn't need to
	// know the bot's username.
	parts := strings.Fields(strings.TrimSpace(upd.Message.Text))
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return
	}

	// Go pattern — strings.SplitN WITH LIMIT 2:
	//
	// SplitN(s, "@", 2) splits into at most 2 parts. For "/standings@BotName"
	// this gives ["/standings", "BotName"]. For "/standings" (no @) it gives
	// ["/standings"]. Taking [0] always gives us the clean command.
	command := strings.ToLower(strings.SplitN(parts[0], "@", 2)[0])
	args := parts[1:]

	response, err := h.dispatchCommand(ctx, command, args)
	if err != nil {
		slog.Warn("command error", "command", command, "error", err)
		response = "Sorry, something went wrong. Try again later."
	}

	// Empty response means the command was unrecognized — silently ignore.
	if response == "" {
		return
	}

	if err := h.tg.SendRaw(ctx, h.chatID, response); err != nil {
		slog.Error("send reply failed", "command", command, "error", err)
	}
}

// dispatchCommand routes a parsed command to its handler function.
//
// Go pattern — SWITCH AS COMMAND ROUTER:
//
// A switch on the command string is the simplest possible router. For 4
// commands, a map[string]handlerFunc would be over-engineering. If this
// grows to 10+ commands, a map-based dispatcher would be cleaner.
func (h *Handler) dispatchCommand(ctx context.Context, command string, args []string) (string, error) {
	switch command {
	case "/standings":
		return handleStandings(ctx, h.store, h.leagueID)
	case "/streak":
		return handleStreak(ctx, h.stats)
	case "/history":
		return handleHistory(ctx, h.stats, h.store, h.leagueID, args)
	case "/deadline":
		return handleDeadline(ctx, h.fpl)
	default:
		// Unknown command — return empty string to silently ignore.
		return "", nil
	}
}
