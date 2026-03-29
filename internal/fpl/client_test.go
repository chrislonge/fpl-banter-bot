// ---------------------------------------------------------------------------
// Go concept — TEST FILE NAMING:
//
// Go test files must end in _test.go. The `go test` command compiles and
// runs these files. The `go build` command ignores them entirely — they
// are never included in your production binary. This is enforced by the
// toolchain, not convention.
//
// Go concept — BLACK-BOX TESTING (package fpl_test):
//
// Notice the package is `fpl_test`, NOT `fpl`. This means:
//   - Tests can only access EXPORTED symbols (capitalized names).
//   - It tests the public API as an external consumer would see it.
//   - It prevents tests from depending on internal implementation details.
//
// Go also allows `package fpl` (white-box) tests in the same directory.
// Use white-box tests when you need to test unexported helpers directly.
// We use black-box here because the public API is what matters.
//
// Go concept — TABLE-DRIVEN TESTS:
//
// Instead of writing one test function per scenario, we define a slice of
// test cases (a "table") and loop over them with t.Run(). Benefits:
//   - Adding a new case is one struct literal, not a new function.
//   - The test name appears in output, so failures pinpoint the exact case.
//   - It separates test DATA from test LOGIC — easier to read and maintain.
//
// This is THE standard testing pattern in Go. You'll see it in the stdlib,
// in popular open-source projects, and in every Go interview.
//
// Go concept — httptest.NewServer:
//
// net/http/httptest creates a real HTTP server on a random local port.
// The FPL client talks to it over real HTTP — request construction,
// header setting, status code checking, and JSON decoding all execute
// exactly as they would against the real FPL API. The only difference is
// the URL. This is far more reliable than mocking the http.Client interface.
// ---------------------------------------------------------------------------
package fpl_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
)

// ---------------------------------------------------------------------------
// TestGetBootstrap
// ---------------------------------------------------------------------------

func TestGetBootstrap(t *testing.T) {
	// Each entry in this slice is one test case. The struct is defined
	// inline (an "anonymous struct") — this is idiomatic for table tests
	// because the struct shape is only used here.
	tests := []struct {
		name       string // Descriptive name shown in test output
		statusCode int    // HTTP status code the fake server returns
		body       string // JSON body the fake server returns
		wantErr    bool   // Do we expect an error?
		wantEvents int    // Expected number of events (only checked if no error)
		wantTeams  int    // Expected number of teams (only checked if no error)
		wantPlayers int   // Expected number of players (only checked if no error)
	}{
		{
			name:       "valid response with events and teams",
			statusCode: http.StatusOK,
			body: `{
				"events": [
					{"id": 1, "name": "Gameweek 1", "finished": true, "data_checked": true, "is_previous": false, "is_current": false, "is_next": false},
					{"id": 2, "name": "Gameweek 2", "finished": false, "data_checked": false, "is_previous": false, "is_current": true, "is_next": false}
				],
				"teams": [
					{"id": 1, "name": "Arsenal", "short_name": "ARS"}
				],
				"elements": [
					{"id": 430, "web_name": "Haaland"}
				]
			}`,
			wantErr:    false,
			wantEvents: 2,
			wantTeams:  1,
			wantPlayers: 1,
		},
		{
			name:       "server error returns error",
			statusCode: http.StatusInternalServerError,
			body:       `{}`,
			wantErr:    true,
		},
		{
			name:       "malformed JSON returns error",
			statusCode: http.StatusOK,
			body:       `{not valid json}`,
			wantErr:    true,
		},
	}

	// Range over the table. `tt` is short for "table test" — a Go convention.
	for _, tt := range tests {
		// t.Run creates a named subtest. When this fails, Go prints:
		//   --- FAIL: TestGetBootstrap/valid_response_with_events_and_teams
		// You can run a single subtest with:
		//   go test -run TestGetBootstrap/server_error
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake HTTP server that returns our canned response.
			// httptest.NewServer starts a real HTTP server on localhost.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			// Always close the server when the subtest finishes.
			defer srv.Close()

			// Create an FPL client pointing at our fake server.
			// srv.Client() returns an *http.Client pre-configured to
			// talk to this specific test server.
			client := fpl.NewClient(srv.URL, srv.Client(), nil)

			resp, err := client.GetBootstrap(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return // Don't check response fields when we expect an error.
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := len(resp.Events); got != tt.wantEvents {
				t.Errorf("events count = %d, want %d", got, tt.wantEvents)
			}
			if got := len(resp.Teams); got != tt.wantTeams {
				t.Errorf("teams count = %d, want %d", got, tt.wantTeams)
			}
			if got := len(resp.Elements); got != tt.wantPlayers {
				t.Errorf("elements count = %d, want %d", got, tt.wantPlayers)
			}
		})
	}
}

// TestGetBootstrapFieldMapping verifies that specific JSON fields map
// to the correct struct fields. This catches struct tag typos.
func TestGetBootstrapFieldMapping(t *testing.T) {
	body := `{
		"events": [{
			"id": 5,
			"name": "Gameweek 5",
			"deadline_time": "2025-09-20T10:00:00Z",
			"finished": true,
			"data_checked": true,
			"is_previous": true,
			"is_current": false,
			"is_next": false,
			"average_entry_score": 52,
			"highest_score": 134,
			"most_captained": 328,
			"chip_plays": [{"chip_name": "bboost", "num_played": 150000}]
		}],
		"teams": [{
			"id": 1,
			"name": "Arsenal",
			"short_name": "ARS",
			"code": 3,
			"strength": 5,
			"position": 1
		}],
		"elements": [{
			"id": 430,
			"web_name": "Haaland"
		}]
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	resp, err := client.GetBootstrap(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify event fields.
	ev := resp.Events[0]
	if ev.ID != 5 {
		t.Errorf("Event.ID = %d, want 5", ev.ID)
	}
	if ev.Name != "Gameweek 5" {
		t.Errorf("Event.Name = %q, want %q", ev.Name, "Gameweek 5")
	}
	if ev.DeadlineTime != "2025-09-20T10:00:00Z" {
		t.Errorf("Event.DeadlineTime = %q, want %q", ev.DeadlineTime, "2025-09-20T10:00:00Z")
	}
	if !ev.Finished {
		t.Error("Event.Finished = false, want true")
	}
	if !ev.DataChecked {
		t.Error("Event.DataChecked = false, want true")
	}
	if !ev.IsPrevious {
		t.Error("Event.IsPrevious = false, want true")
	}
	if ev.AverageScore != 52 {
		t.Errorf("Event.AverageScore = %d, want 52", ev.AverageScore)
	}
	if ev.HighestScore != 134 {
		t.Errorf("Event.HighestScore = %d, want 134", ev.HighestScore)
	}
	if ev.MostCaptained != 328 {
		t.Errorf("Event.MostCaptained = %d, want 328", ev.MostCaptained)
	}
	if len(ev.ChipPlays) != 1 || ev.ChipPlays[0].ChipName != "bboost" {
		t.Errorf("Event.ChipPlays = %+v, want [{bboost 150000}]", ev.ChipPlays)
	}

	// Verify team fields.
	tm := resp.Teams[0]
	if tm.ID != 1 {
		t.Errorf("Team.ID = %d, want 1", tm.ID)
	}
	if tm.Name != "Arsenal" {
		t.Errorf("Team.Name = %q, want %q", tm.Name, "Arsenal")
	}
	if tm.ShortName != "ARS" {
		t.Errorf("Team.ShortName = %q, want %q", tm.ShortName, "ARS")
	}
	player := resp.Elements[0]
	if player.ID != 430 {
		t.Errorf("Element.ID = %d, want 430", player.ID)
	}
	if player.WebName != "Haaland" {
		t.Errorf("Element.WebName = %q, want %q", player.WebName, "Haaland")
	}
}

// TestGetBootstrapUserAgent verifies the client sets the expected User-Agent header.
func TestGetBootstrapUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"events":[],"teams":[]}`))
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	_, err := client.GetBootstrap(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotUA != "fpl-banter-bot/1.0" {
		t.Errorf("User-Agent = %q, want %q", gotUA, "fpl-banter-bot/1.0")
	}
}

// ---------------------------------------------------------------------------
// TestGetEventStatus
// ---------------------------------------------------------------------------

func TestGetEventStatus(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         string
		wantErr      bool
		wantStatuses int
		wantLeagues  string
	}{
		{
			name:       "valid response",
			statusCode: http.StatusOK,
			body: `{
				"status": [
					{"bonus_added": true, "date": "2025-09-20", "event": 5, "points": "r"},
					{"bonus_added": false, "date": "2025-09-21", "event": 5, "points": "r"}
				],
				"leagues": "Updated"
			}`,
			wantErr:      false,
			wantStatuses: 2,
			wantLeagues:  "Updated",
		},
		{
			name:       "server error",
			statusCode: http.StatusServiceUnavailable,
			body:       `{}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			client := fpl.NewClient(srv.URL, srv.Client(), nil)
			resp, err := client.GetEventStatus(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := len(resp.Status); got != tt.wantStatuses {
				t.Errorf("status count = %d, want %d", got, tt.wantStatuses)
			}
			if resp.Leagues != tt.wantLeagues {
				t.Errorf("leagues = %q, want %q", resp.Leagues, tt.wantLeagues)
			}

			// Verify field mapping on the first status entry.
			if len(resp.Status) > 0 {
				s := resp.Status[0]
				if !s.BonusAdded {
					t.Error("Status[0].BonusAdded = false, want true")
				}
				if s.Event != 5 {
					t.Errorf("Status[0].Event = %d, want 5", s.Event)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestGetH2HStandings
// ---------------------------------------------------------------------------

func TestGetH2HStandings(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		body        string
		wantErr     bool
		wantResults int
	}{
		{
			name:       "valid single page",
			statusCode: http.StatusOK,
			body: `{
				"league": {"id": 916670, "name": "Capital FC", "scoring": "h"},
				"standings": {
					"has_next": false,
					"page": 1,
					"results": [
						{
							"id": 1, "entry": 12345, "player_name": "Chris",
							"entry_name": "FC Banter", "rank": 1, "last_rank": 2,
							"total": 30, "matches_played": 10, "matches_won": 10,
							"matches_drawn": 0, "matches_lost": 0, "points_for": 650
						}
					]
				}
			}`,
			wantErr:     false,
			wantResults: 1,
		},
		{
			name:       "league not found",
			statusCode: http.StatusNotFound,
			body:       `Not Found`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			client := fpl.NewClient(srv.URL, srv.Client(), nil)
			resp, err := client.GetH2HStandings(context.Background(), 916670, 1)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := len(resp.Standings.Results); got != tt.wantResults {
				t.Errorf("results count = %d, want %d", got, tt.wantResults)
			}

			// Verify field mapping.
			if len(resp.Standings.Results) > 0 {
				e := resp.Standings.Results[0]
				if e.EntryID != 12345 {
					t.Errorf("EntryID = %d, want 12345", e.EntryID)
				}
				if e.PlayerName != "Chris" {
					t.Errorf("PlayerName = %q, want %q", e.PlayerName, "Chris")
				}
				if e.Total != 30 {
					t.Errorf("Total = %d, want 30", e.Total)
				}
			}
		})
	}
}

// TestGetH2HStandingsURLPath verifies the client builds the correct URL
// including the league ID and page parameter.
func TestGetH2HStandingsURLPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL includes path + query string
		gotPath = r.URL.String()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"league":{"id":1},"standings":{"has_next":false,"page":1,"results":[]}}`))
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	_, err := client.GetH2HStandings(context.Background(), 916670, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := "/leagues-h2h/916670/standings/?page_standings=3"
	if gotPath != wantPath {
		t.Errorf("URL path = %q, want %q", gotPath, wantPath)
	}
}

// ---------------------------------------------------------------------------
// TestGetAllH2HStandings
// ---------------------------------------------------------------------------

// TestGetAllH2HStandings verifies that the pagination helper correctly
// follows has_next and merges results from multiple pages.
func TestGetAllH2HStandings(t *testing.T) {
	// This handler inspects the page_standings query param to return
	// different responses for each page — simulating real pagination.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page_standings")
		switch page {
		case "1", "":
			_, _ = w.Write([]byte(`{
				"league": {"id": 916670, "name": "Capital FC", "scoring": "h"},
				"standings": {
					"has_next": true,
					"page": 1,
					"results": [
						{"entry": 100, "player_name": "Alice"},
						{"entry": 200, "player_name": "Bob"}
					]
				}
			}`))
		case "2":
			_, _ = w.Write([]byte(`{
				"league": {"id": 916670, "name": "Capital FC", "scoring": "h"},
				"standings": {
					"has_next": false,
					"page": 2,
					"results": [
						{"entry": 300, "player_name": "Charlie"}
					]
				}
			}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	resp, err := client.GetAllH2HStandings(context.Background(), 916670)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have merged all 3 results from 2 pages.
	if got := len(resp.Standings.Results); got != 3 {
		t.Fatalf("results count = %d, want 3", got)
	}

	// Verify the order is preserved (page 1 results first, then page 2).
	names := []string{
		resp.Standings.Results[0].PlayerName,
		resp.Standings.Results[1].PlayerName,
		resp.Standings.Results[2].PlayerName,
	}
	wantNames := []string{"Alice", "Bob", "Charlie"}
	for i, got := range names {
		if got != wantNames[i] {
			t.Errorf("result[%d].PlayerName = %q, want %q", i, got, wantNames[i])
		}
	}

	// has_next should be false on the merged result.
	if resp.Standings.HasNext {
		t.Error("HasNext = true, want false after merging all pages")
	}

	// League metadata should come from the first page.
	if resp.League.Name != "Capital FC" {
		t.Errorf("League.Name = %q, want %q", resp.League.Name, "Capital FC")
	}
}

// ---------------------------------------------------------------------------
// TestGetManagerHistory
// ---------------------------------------------------------------------------

func TestGetH2HMatches(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		body        string
		wantErr     bool
		wantResults int
	}{
		{
			name:       "valid single page",
			statusCode: http.StatusOK,
			body: `{
				"has_next": false,
				"page": 1,
				"results": [
					{
						"id": 13799773,
						"entry_1_entry": 4350338,
						"entry_1_name": "doyourwirtz",
						"entry_1_player_name": "William Denman",
						"entry_1_points": 55,
						"entry_1_win": 0,
						"entry_1_draw": 1,
						"entry_1_loss": 0,
						"entry_1_total": 1,
						"entry_2_entry": 4693819,
						"entry_2_name": "Declan Twice!",
						"entry_2_player_name": "Chris Longe",
						"entry_2_points": 55,
						"entry_2_win": 0,
						"entry_2_draw": 1,
						"entry_2_loss": 0,
						"entry_2_total": 1,
						"is_knockout": false,
						"league": 916670,
						"winner": null,
						"seed_value": null,
						"event": 1,
						"tiebreak": null,
						"is_bye": false,
						"knockout_name": ""
					}
				]
			}`,
			wantResults: 1,
		},
		{
			name:       "invalid event",
			statusCode: http.StatusBadRequest,
			body:       `{"event":[{"message":"Not a valid event id","code":"invalid"}]}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			client := fpl.NewClient(srv.URL, srv.Client(), nil)
			resp, err := client.GetH2HMatches(context.Background(), 916670, 1, 1)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := len(resp.Results); got != tt.wantResults {
				t.Fatalf("results count = %d, want %d", got, tt.wantResults)
			}

			match := resp.Results[0]
			if match.Entry1Entry != 4350338 {
				t.Errorf("Entry1Entry = %d, want 4350338", match.Entry1Entry)
			}
			if match.Entry2PlayerName != "Chris Longe" {
				t.Errorf("Entry2PlayerName = %q, want %q", match.Entry2PlayerName, "Chris Longe")
			}
			if match.IsBye {
				t.Error("IsBye = true, want false")
			}
			if match.Winner != nil {
				t.Errorf("Winner = %v, want nil for a draw", *match.Winner)
			}
		})
	}
}

func TestGetH2HMatchesURLPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"has_next":false,"page":1,"results":[]}`))
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	_, err := client.GetH2HMatches(context.Background(), 916670, 3, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := "/leagues-h2h-matches/league/916670/?page=3&event=7"
	if gotPath != wantPath {
		t.Errorf("URL path = %q, want %q", gotPath, wantPath)
	}
}

func TestGetAllH2HMatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "1", "":
			_, _ = w.Write([]byte(`{
				"has_next": true,
				"page": 1,
				"results": [
					{"id": 1, "entry_1_entry": 100, "entry_1_points": 60, "entry_2_entry": 200, "entry_2_points": 58, "league": 916670, "event": 5, "is_bye": false, "knockout_name": ""}
				]
			}`))
		case "2":
			_, _ = w.Write([]byte(`{
				"has_next": false,
				"page": 2,
				"results": [
					{"id": 2, "entry_1_entry": 300, "entry_1_points": 48, "entry_2_entry": 400, "entry_2_points": 52, "league": 916670, "event": 5, "is_bye": false, "knockout_name": ""}
				]
			}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	resp, err := client.GetAllH2HMatches(context.Background(), 916670, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(resp.Results); got != 2 {
		t.Fatalf("results count = %d, want 2", got)
	}
	if resp.HasNext {
		t.Error("HasNext = true, want false after merging all pages")
	}
	if resp.Page != 2 {
		t.Errorf("Page = %d, want 2", resp.Page)
	}
	if resp.Results[1].Entry2Entry != 400 {
		t.Errorf("second page result not merged correctly: %+v", resp.Results[1])
	}
}

func TestGetH2HStandings_GameUpdating503ReturnsSentinelError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("The game is being updated."))
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	_, err := client.GetH2HStandings(context.Background(), 916670, 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, fpl.ErrGameUpdating) {
		t.Fatalf("errors.Is(err, ErrGameUpdating) = false, err = %v", err)
	}
	if !fpl.IsGameUpdating(err) {
		t.Fatalf("IsGameUpdating(err) = false, err = %v", err)
	}
}

func TestGetH2HStandings_Generic503DoesNotReturnGameUpdatingSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("Service temporarily unavailable"))
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	_, err := client.GetH2HStandings(context.Background(), 916670, 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, fpl.ErrGameUpdating) {
		t.Fatalf("errors.Is(err, ErrGameUpdating) = true, err = %v", err)
	}
}

func TestGetManagerHistory(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		body        string
		wantErr     bool
		wantCurrent int
		wantChips   int
	}{
		{
			name:       "valid response with history and chips",
			statusCode: http.StatusOK,
			body: `{
				"current": [
					{"event": 1, "points": 65, "total_points": 65, "rank": 100000, "overall_rank": 150000, "bank": 5, "value": 1000, "event_transfers": 0, "event_transfers_cost": 0, "points_on_bench": 12},
					{"event": 2, "points": 48, "total_points": 113, "rank": 500000, "overall_rank": 200000, "bank": 5, "value": 1001, "event_transfers": 1, "event_transfers_cost": 0, "points_on_bench": 8}
				],
				"chips": [
					{"event": 1, "name": "wildcard"}
				]
			}`,
			wantErr:     false,
			wantCurrent: 2,
			wantChips:   1,
		},
		{
			name:       "manager not found",
			statusCode: http.StatusNotFound,
			body:       `Not Found`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			client := fpl.NewClient(srv.URL, srv.Client(), nil)
			resp, err := client.GetManagerHistory(context.Background(), 12345)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := len(resp.Current); got != tt.wantCurrent {
				t.Errorf("current count = %d, want %d", got, tt.wantCurrent)
			}
			if got := len(resp.Chips); got != tt.wantChips {
				t.Errorf("chips count = %d, want %d", got, tt.wantChips)
			}

			// Verify field mapping.
			if len(resp.Current) > 0 {
				gw := resp.Current[0]
				if gw.Points != 65 {
					t.Errorf("Current[0].Points = %d, want 65", gw.Points)
				}
				if gw.PointsOnBench != 12 {
					t.Errorf("Current[0].PointsOnBench = %d, want 12", gw.PointsOnBench)
				}
			}
			if len(resp.Chips) > 0 {
				chip := resp.Chips[0]
				if chip.Name != "wildcard" {
					t.Errorf("Chips[0].Name = %q, want %q", chip.Name, "wildcard")
				}
			}
		})
	}
}

// TestGetManagerHistoryURLPath verifies the URL includes the manager ID.
func TestGetManagerHistoryURLPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"current":[],"chips":[]}`))
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	_, _ = client.GetManagerHistory(context.Background(), 99999)

	if gotPath != "/entry/99999/history/" {
		t.Errorf("URL path = %q, want %q", gotPath, "/entry/99999/history/")
	}
}

// ---------------------------------------------------------------------------
// TestGetManagerPicks
// ---------------------------------------------------------------------------

func TestGetManagerPicks(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
		wantChip   *string // nil means we expect ActiveChip to be nil
		wantPicks  int
	}{
		{
			name:       "response with active chip",
			statusCode: http.StatusOK,
			body: `{
				"active_chip": "bboost",
				"picks": [
					{"element": 1, "position": 1, "multiplier": 1, "is_captain": false, "is_vice_captain": false},
					{"element": 2, "position": 2, "multiplier": 2, "is_captain": true, "is_vice_captain": false}
				],
				"automatic_subs": [],
				"entry_history": {"event": 5, "points": 72, "total_points": 350}
			}`,
			wantErr:   false,
			wantChip:  strPtr("bboost"),
			wantPicks: 2,
		},
		{
			// This test verifies the *string pointer behavior:
			// JSON null should decode to a nil pointer, not an empty string.
			name:       "response with null active chip",
			statusCode: http.StatusOK,
			body: `{
				"active_chip": null,
				"picks": [{"element": 1, "position": 1, "multiplier": 1, "is_captain": false, "is_vice_captain": false}],
				"automatic_subs": [],
				"entry_history": {"event": 5, "points": 45, "total_points": 300}
			}`,
			wantErr:   false,
			wantChip:  nil,
			wantPicks: 1,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			body:       `{}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			client := fpl.NewClient(srv.URL, srv.Client(), nil)
			resp, err := client.GetManagerPicks(context.Background(), 12345, 5)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Compare ActiveChip pointers.
			if tt.wantChip == nil {
				if resp.ActiveChip != nil {
					t.Errorf("ActiveChip = %q, want nil", *resp.ActiveChip)
				}
			} else {
				if resp.ActiveChip == nil {
					t.Fatalf("ActiveChip = nil, want %q", *tt.wantChip)
				}
				if *resp.ActiveChip != *tt.wantChip {
					t.Errorf("ActiveChip = %q, want %q", *resp.ActiveChip, *tt.wantChip)
				}
			}

			if got := len(resp.Picks); got != tt.wantPicks {
				t.Errorf("picks count = %d, want %d", got, tt.wantPicks)
			}

			// Verify captain detection.
			if len(resp.Picks) >= 2 {
				if resp.Picks[1].IsCaptain != true {
					t.Error("Picks[1].IsCaptain = false, want true")
				}
				if resp.Picks[1].Multiplier != 2 {
					t.Errorf("Picks[1].Multiplier = %d, want 2", resp.Picks[1].Multiplier)
				}
			}
		})
	}
}

// TestGetManagerPicksURLPath verifies the URL includes both manager ID and event.
func TestGetManagerPicksURLPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"active_chip":null,"picks":[],"automatic_subs":[],"entry_history":{"event":10}}`))
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	_, _ = client.GetManagerPicks(context.Background(), 42, 10)

	if gotPath != "/entry/42/event/10/picks/" {
		t.Errorf("URL path = %q, want %q", gotPath, "/entry/42/event/10/picks/")
	}
}

func TestGetEventLive(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
		wantCount  int
	}{
		{
			name:       "valid response with element points",
			statusCode: http.StatusOK,
			body: `{
				"elements": [
					{"id": 430, "stats": {"total_points": 13}},
					{"id": 328, "stats": {"total_points": 2}}
				]
			}`,
			wantCount: 2,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			body:       `{}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			client := fpl.NewClient(srv.URL, srv.Client(), nil)
			resp, err := client.GetEventLive(context.Background(), 5)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := len(resp.Elements); got != tt.wantCount {
				t.Fatalf("len(elements) = %d, want %d", got, tt.wantCount)
			}
			if resp.Elements[0].ID != 430 {
				t.Errorf("Elements[0].ID = %d, want 430", resp.Elements[0].ID)
			}
			if resp.Elements[0].Stats.TotalPoints == nil || *resp.Elements[0].Stats.TotalPoints != 13 {
				t.Errorf("Elements[0].Stats.TotalPoints = %v, want 13", resp.Elements[0].Stats.TotalPoints)
			}
		})
	}
}

func TestGetEventLiveURLPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"elements":[]}`))
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)
	_, _ = client.GetEventLive(context.Background(), 7)

	if gotPath != "/event/7/live/" {
		t.Errorf("URL path = %q, want %q", gotPath, "/event/7/live/")
	}
}

// ---------------------------------------------------------------------------
// TestContextCancellation
// ---------------------------------------------------------------------------
//
// This test proves that context.Context propagation works. If the context is
// cancelled before the HTTP call, the client should return an error immediately
// rather than making a network request.
// ---------------------------------------------------------------------------

func TestContextCancellation(t *testing.T) {
	// Create a server that should never be reached.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server was called despite cancelled context")
	}))
	defer srv.Close()

	client := fpl.NewClient(srv.URL, srv.Client(), nil)

	// Create a context and immediately cancel it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before making any call.

	// Every method should fail because the context is already cancelled.
	if _, err := client.GetBootstrap(ctx); err == nil {
		t.Error("GetBootstrap: expected error from cancelled context, got nil")
	}
	if _, err := client.GetEventStatus(ctx); err == nil {
		t.Error("GetEventStatus: expected error from cancelled context, got nil")
	}
	if _, err := client.GetEventLive(ctx, 1); err == nil {
		t.Error("GetEventLive: expected error from cancelled context, got nil")
	}
	if _, err := client.GetH2HStandings(ctx, 1, 1); err == nil {
		t.Error("GetH2HStandings: expected error from cancelled context, got nil")
	}
	if _, err := client.GetH2HMatches(ctx, 1, 1, 1); err == nil {
		t.Error("GetH2HMatches: expected error from cancelled context, got nil")
	}
	if _, err := client.GetManagerHistory(ctx, 1); err == nil {
		t.Error("GetManagerHistory: expected error from cancelled context, got nil")
	}
	if _, err := client.GetManagerPicks(ctx, 1, 1); err == nil {
		t.Error("GetManagerPicks: expected error from cancelled context, got nil")
	}
}

// ---------------------------------------------------------------------------
// Structured logging tests
// ---------------------------------------------------------------------------

// TestClient_LogOutput verifies that the FPL client emits structured log
// fields (path, status, duration_ms) when a logger is injected. This uses
// a buffer-backed slog.JSONHandler to capture output without writing to
// stdout — the same pattern the plan specifies for verifying component
// logging behavior.
func TestClient_LogOutput(t *testing.T) {
	t.Run("success path logs path, status, and duration_ms", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `{"events":[]}`)
		}))
		defer srv.Close()

		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		client := fpl.NewClient(srv.URL, srv.Client(), logger)
		_, _ = client.GetBootstrap(context.Background())

		// Parse the log output — Debug-level success logs should contain our fields.
		var entry map[string]any
		if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
			t.Fatalf("log output is not valid JSON: %v\nraw: %s", err, buf.String())
		}

		// Verify expected fields are present.
		if entry["path"] == nil {
			t.Error("expected 'path' field in log output")
		}
		if entry["status"] == nil {
			t.Error("expected 'status' field in log output")
		}
		if entry["duration_ms"] == nil {
			t.Error("expected 'duration_ms' field in log output")
		}
		if entry["msg"] != "fpl api call" {
			t.Errorf("msg = %q, want %q", entry["msg"], "fpl api call")
		}
	})

	t.Run("non-200 path logs path, status, and duration_ms", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "The game is being updated.")
		}))
		defer srv.Close()

		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		client := fpl.NewClient(srv.URL, srv.Client(), logger)
		_, _ = client.GetBootstrap(context.Background())

		var entry map[string]any
		if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
			t.Fatalf("log output is not valid JSON: %v\nraw: %s", err, buf.String())
		}

		if entry["msg"] != "fpl api non-200" {
			t.Errorf("msg = %q, want %q", entry["msg"], "fpl api non-200")
		}
		if entry["status"] == nil {
			t.Error("expected 'status' field in non-200 log output")
		}
		if entry["duration_ms"] == nil {
			t.Error("expected 'duration_ms' field in non-200 log output")
		}
	})

	t.Run("network error path logs without status", func(t *testing.T) {
		// Point at a URL that will fail immediately — closed server.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.Close() // Close immediately to force a connection error.

		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		client := fpl.NewClient(srv.URL, srv.Client(), logger)
		_, _ = client.GetBootstrap(context.Background())

		var entry map[string]any
		if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
			t.Fatalf("log output is not valid JSON: %v\nraw: %s", err, buf.String())
		}

		if entry["msg"] != "fpl api error" {
			t.Errorf("msg = %q, want %q", entry["msg"], "fpl api error")
		}
		if entry["duration_ms"] == nil {
			t.Error("expected 'duration_ms' field in error log output")
		}
		if entry["error"] == nil {
			t.Error("expected 'error' field in error log output")
		}
		// Crucially: no 'status' field since resp was nil.
		if entry["status"] != nil {
			t.Error("expected no 'status' field in network error log output")
		}
	})
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// strPtr returns a pointer to a string. This is a common Go helper for
// tests that need to compare *string values in table-driven tests.
//
// Why is this needed? In Go, you can't take the address of a string
// literal directly: &"hello" is a compile error. You need a variable
// or a helper function.
func strPtr(s string) *string {
	return &s
}
