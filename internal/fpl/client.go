package fpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------
//
// Go concept — STRUCT AS A "CLASS":
//
// Go doesn't have classes. Instead, you define a struct (data) and attach
// methods to it. The combination of struct + methods is Go's equivalent of
// a class. The fields below are like private instance variables — they're
// lowercase, so they're unexported (only accessible within this package).
//
// Go concept — DEPENDENCY INJECTION VIA CONSTRUCTOR:
//
// NewClient accepts a base URL and an *http.Client from the outside rather
// than hardcoding them. This is the Dependency Inversion Principle:
//
//   - Production code passes the real FPL URL and a configured HTTP client.
//   - Tests pass an httptest.Server URL and its built-in client.
//
// The FPL client doesn't know or care which one it's talking to. This makes
// the code testable without mocking frameworks — a key Go philosophy.
//
// Compare to Swift/Kotlin: this is like injecting a URLSession/OkHttpClient
// through a constructor instead of using URLSession.shared / OkHttpClient().
// ---------------------------------------------------------------------------

// Client is an HTTP client for the Fantasy Premier League API.
// It is safe for concurrent use because the underlying *http.Client is
// safe for concurrent use.
type Client struct {
	baseURL    string       // e.g., "https://fantasy.premierleague.com/api"
	httpClient *http.Client // the actual HTTP transport
}

// maxH2HStandingsPages is a defensive guard to prevent infinite pagination
// if the upstream API misbehaves and never returns has_next=false.
const maxH2HStandingsPages = 1000

// maxH2HMatchesPages is the same defensive guard for the H2H matches API.
const maxH2HMatchesPages = 1000

// ErrGameUpdating is returned when the FPL API temporarily serves the
// "The game is being updated." maintenance message instead of JSON.
var ErrGameUpdating = errors.New("fpl: game is updating")

// APIError preserves HTTP-level context for non-200 FPL responses while still
// allowing callers to classify known transient conditions with errors.Is.
type APIError struct {
	StatusCode int
	Path       string
	Body       string
	Err        error
}

func (e *APIError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("unexpected status %d for %s: %s", e.StatusCode, e.Path, e.Body)
	}
	return fmt.Sprintf("unexpected status %d for %s", e.StatusCode, e.Path)
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// IsGameUpdating reports whether err represents the transient FPL maintenance
// window where endpoints return "The game is being updated.".
func IsGameUpdating(err error) bool {
	return errors.Is(err, ErrGameUpdating)
}

// NewClient creates a new FPL API client.
//
// baseURL is the API root (e.g., "https://fantasy.premierleague.com/api").
// httpClient allows callers to configure timeouts and transport settings.
// If nil, a default client with a 30-second timeout is used.
//
// Why accept *http.Client instead of creating one internally?
//   - Tests can pass a client wired to an httptest.Server (no real network).
//   - The caller owns timeout policy — the FPL client shouldn't decide this.
//   - Single Responsibility: this client does FPL-specific logic, not HTTP config.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		// strings.TrimRight removes trailing slashes so we can safely
		// build URLs with baseURL + "/" + path without double slashes.
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

// ---------------------------------------------------------------------------
// Internal helper: do
// ---------------------------------------------------------------------------
//
// Go concept — METHOD ON A STRUCT:
//
//	func (c *Client) do(...) error { ... }
//	       ^^^^^^^^
//	       This is the "receiver." It's like `self` in Swift or `this` in
//	       Kotlin. The method is attached to *Client (pointer receiver),
//	       meaning it can read the struct's fields. We use a pointer receiver
//	       (*Client, not Client) because copying the struct on every call
//	       would be wasteful and could break if we later add mutable state.
//
// Go concept — json.NewDecoder vs json.Unmarshal:
//
//   - json.Unmarshal(data, &target): reads the ENTIRE body into []byte first,
//     then parses. Simple, but doubles memory usage for large responses.
//   - json.NewDecoder(reader).Decode(&target): streams directly from the
//     io.Reader (the HTTP response body). Parses as it reads — one pass,
//     no intermediate buffer.
//
// We use NewDecoder because bootstrap-static/ is ~1.3 MB. Streaming avoids
// allocating a second 1.3 MB byte slice just to parse it.
//
// Go concept — http.NewRequestWithContext:
//
// This binds a context.Context to the HTTP request. If the context is
// cancelled (e.g., the bot is shutting down via SIGTERM), the HTTP call
// aborts immediately instead of hanging until the timeout expires. Always
// use NewRequestWithContext, never the older NewRequest + req.WithContext.
//
// Go concept — defer resp.Body.Close():
//
// In Go, HTTP response bodies are io.ReadClosers — they hold a network
// connection open until you close them. If you forget to close, you leak
// connections and eventually run out. `defer` schedules Close() to run
// when the enclosing function returns, regardless of whether it returns
// normally or via an error. This is Go's version of try/finally.
//
// Rule: always defer Body.Close() immediately after a successful Do() call.
//
// Go concept — ERROR WRAPPING with fmt.Errorf and %w:
//
//	return fmt.Errorf("fetching %s: %w", path, err)
//
// The %w verb wraps the original error inside a new error that adds context.
// Callers can later "unwrap" to inspect the original with errors.Is() or
// errors.As(). This is Go's explicit, type-safe version of exception chaining.
// Always add context about WHAT failed, not just that something failed.
// ---------------------------------------------------------------------------

// do executes a GET request to the given path and JSON-decodes the response
// into target, which must be a pointer to a struct.
func (c *Client) do(ctx context.Context, path string, target any) error {
	url := c.baseURL + "/" + path

	// Create the request with the caller's context attached.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request for %s: %w", path, err)
	}

	// Set a User-Agent so the FPL servers know who's calling. This is good
	// API citizenship — it helps server operators identify traffic sources.
	req.Header.Set("User-Agent", "fpl-banter-bot/1.0")

	// Execute the request. Do() respects the context — if ctx is cancelled,
	// this returns immediately with an error.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", path, err)
	}
	defer resp.Body.Close()

	// Check for non-200 status codes. The FPL API returns 200 on success,
	// 404 for bad IDs, and 503 during peak load.
	if resp.StatusCode != http.StatusOK {
		const maxErrorBodyBytes = 4096

		var bodySnippet string
		if resp.Body != nil {
			limited := io.LimitReader(resp.Body, maxErrorBodyBytes)
			if b, readErr := io.ReadAll(limited); readErr == nil && len(b) > 0 {
				bodySnippet = strings.TrimSpace(string(b))
			}
		}

		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Path:       path,
			Body:       bodySnippet,
		}
		if resp.StatusCode == http.StatusServiceUnavailable &&
			strings.Contains(strings.ToLower(bodySnippet), "game is being updated") {
			apiErr.Err = ErrGameUpdating
		}

		return apiErr
	}

	// Stream-decode the JSON response directly from the body.
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Public methods
// ---------------------------------------------------------------------------
//
// Each method is a thin wrapper around do(). This pattern keeps the public
// API clean and type-safe: each method returns a specific response struct,
// not a generic interface{}.
//
// Go concept — RETURNING (value, error):
//
// Go functions that can fail return two values: the result and an error.
// The caller MUST check the error before using the result. This is Go's
// alternative to exceptions — errors are values, not control flow.
//
//	resp, err := client.GetBootstrap(ctx)
//	if err != nil {
//	    // handle error — resp is the zero value, don't use it
//	}
//	// err is nil — safe to use resp
//
// When we return an error, we return the zero value of the response struct
// (e.g., BootstrapResponse{}). In Go, every type has a zero value:
//   - int → 0, string → "", bool → false
//   - struct → all fields set to their zero values
//   - slice → nil (which is safe to range over — it just iterates zero times)
// ---------------------------------------------------------------------------

// GetBootstrap fetches the bootstrap-static data containing all gameweeks
// and teams. This response is ~1.3 MB — callers should cache the result
// rather than fetching it on every poll cycle.
func (c *Client) GetBootstrap(ctx context.Context) (BootstrapResponse, error) {
	var resp BootstrapResponse
	if err := c.do(ctx, "bootstrap-static/", &resp); err != nil {
		return BootstrapResponse{}, err
	}
	return resp, nil
}

// GetEventStatus fetches the current gameweek processing status. This is
// the "heartbeat" endpoint that the poller checks to determine whether
// bonus points and league tables have been updated.
func (c *Client) GetEventStatus(ctx context.Context) (EventStatusResponse, error) {
	var resp EventStatusResponse
	if err := c.do(ctx, "event-status/", &resp); err != nil {
		return EventStatusResponse{}, err
	}
	return resp, nil
}

// GetH2HStandings fetches a single page of head-to-head league standings.
// Pages are 1-indexed. Use GetAllH2HStandings to fetch all pages automatically.
func (c *Client) GetH2HStandings(ctx context.Context, leagueID int, page int) (H2HStandingsResponse, error) {
	var resp H2HStandingsResponse
	path := fmt.Sprintf("leagues-h2h/%d/standings/?page_standings=%d", leagueID, page)
	if err := c.do(ctx, path, &resp); err != nil {
		return H2HStandingsResponse{}, err
	}
	return resp, nil
}

// GetAllH2HStandings fetches ALL pages of H2H standings for a league,
// following the has_next pagination flag until all entries are collected.
//
// Go concept — PAGINATION WITH A FOR LOOP:
//
// Unlike Swift/Kotlin where you might use async sequences or Flow, Go
// uses a simple for loop with a break condition. The loop:
//  1. Fetches page N
//  2. Appends results to a growing slice
//  3. Checks has_next — if false, breaks out
//  4. Otherwise increments page and repeats
//
// Go concept — append():
//
// The built-in append() function adds elements to a slice. Slices in Go
// are backed by arrays — when the array is full, append allocates a new,
// larger array and copies the data. This is similar to ArrayList in Kotlin
// or Array in Swift.
//
//	all = append(all, newItems...)
//	                            ^^^
//	The ... "spreads" the slice — like the spread operator in Kotlin/JS.
func (c *Client) GetAllH2HStandings(ctx context.Context, leagueID int) (H2HStandingsResponse, error) {
	var all H2HStandingsResponse
	page := 1

	for {
		resp, err := c.GetH2HStandings(ctx, leagueID, page)
		if err != nil {
			return H2HStandingsResponse{}, fmt.Errorf("page %d: %w", page, err)
		}

		// On the first page, capture the league metadata.
		if page == 1 {
			all.League = resp.League
		}

		// Merge this page's results into our accumulator.
		all.Standings.Results = append(all.Standings.Results, resp.Standings.Results...)

		if !resp.Standings.HasNext {
			break
		}

		page++
		if page > maxH2HStandingsPages {
			return H2HStandingsResponse{}, fmt.Errorf("reached maximum H2H standings pages (%d) without seeing has_next=false", maxH2HStandingsPages)
		}
	}

	all.Standings.HasNext = false
	all.Standings.Page = page
	return all, nil
}

// GetH2HMatches fetches a single page of head-to-head match results for one
// league and one event. Pages are 1-indexed.
func (c *Client) GetH2HMatches(ctx context.Context, leagueID int, page int, eventID int) (H2HMatchesResponse, error) {
	var resp H2HMatchesResponse
	path := fmt.Sprintf("leagues-h2h-matches/league/%d/?page=%d&event=%d", leagueID, page, eventID)
	if err := c.do(ctx, path, &resp); err != nil {
		return H2HMatchesResponse{}, err
	}
	return resp, nil
}

// GetAllH2HMatches fetches all match-result pages for a single league and
// event, following the has_next pagination flag until all results are merged.
func (c *Client) GetAllH2HMatches(ctx context.Context, leagueID int, eventID int) (H2HMatchesResponse, error) {
	var all H2HMatchesResponse
	page := 1

	for {
		resp, err := c.GetH2HMatches(ctx, leagueID, page, eventID)
		if err != nil {
			return H2HMatchesResponse{}, fmt.Errorf("page %d: %w", page, err)
		}

		all.Results = append(all.Results, resp.Results...)

		if !resp.HasNext {
			break
		}

		page++
		if page > maxH2HMatchesPages {
			return H2HMatchesResponse{}, fmt.Errorf("reached maximum H2H matches pages (%d) without seeing has_next=false", maxH2HMatchesPages)
		}
	}

	all.HasNext = false
	all.Page = page
	return all, nil
}

// GetManagerHistory fetches a manager's gameweek-by-gameweek performance
// and chip usage for the current season.
func (c *Client) GetManagerHistory(ctx context.Context, managerID int) (ManagerHistoryResponse, error) {
	var resp ManagerHistoryResponse
	path := fmt.Sprintf("entry/%d/history/", managerID)
	if err := c.do(ctx, path, &resp); err != nil {
		return ManagerHistoryResponse{}, err
	}
	return resp, nil
}

// GetManagerPicks fetches a manager's team selection for a specific gameweek,
// including captain choice and chip usage.
func (c *Client) GetManagerPicks(ctx context.Context, managerID int, event int) (ManagerPicksResponse, error) {
	var resp ManagerPicksResponse
	path := fmt.Sprintf("entry/%d/event/%d/picks/", managerID, event)
	if err := c.do(ctx, path, &resp); err != nil {
		return ManagerPicksResponse{}, err
	}
	return resp, nil
}
