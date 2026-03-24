package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/stats"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

// ---------------------------------------------------------------------------
// Test fakes
// ---------------------------------------------------------------------------
//
// Go pattern — TEST FAKES VIA INTERFACE:
//
// Each fake implements one consumer-defined interface with in-memory data.
// The fakes are defined in the test file (not in a separate package) because
// they're only needed for testing. In Go, test fakes are intentionally simple
// — just structs with fields for return values and slices for recording calls.
// This is different from Swift/Kotlin mock libraries that auto-generate mocks
// from protocols/interfaces.

type fakeTelegramBot struct {
	mu   sync.Mutex
	sent []sentMessage // records all SendRaw calls
	err  error         // error to return from SendRaw
}

type sentMessage struct {
	chatID string
	text   string
}

func (f *fakeTelegramBot) SendRaw(_ context.Context, chatID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{chatID: chatID, text: text})
	return f.err
}

func (f *fakeTelegramBot) SetWebhook(_ context.Context, _ string) error { return nil }
func (f *fakeTelegramBot) DeleteWebhook(_ context.Context) error        { return nil }

// sentMessages returns a copy of sent messages (thread-safe).
func (f *fakeTelegramBot) sentMessages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

type fakeStatsQuerier struct {
	streaks   []stats.CurrentStreak
	h2hRecord stats.H2HRecord
	streakErr error
	h2hErr    error
}

func (f *fakeStatsQuerier) GetCurrentStreaks(_ context.Context) ([]stats.CurrentStreak, error) {
	return f.streaks, f.streakErr
}

func (f *fakeStatsQuerier) GetH2HRecord(_ context.Context, _, _ int64) (stats.H2HRecord, error) {
	return f.h2hRecord, f.h2hErr
}

type fakeLeagueStore struct {
	standings []store.GameweekStanding
	managers  []store.Manager
	latestID  int
	err       error
}

func (f *fakeLeagueStore) GetStandings(_ context.Context, _ int64, _ int) ([]store.GameweekStanding, error) {
	return f.standings, f.err
}

func (f *fakeLeagueStore) GetManagers(_ context.Context, _ int64) ([]store.Manager, error) {
	return f.managers, f.err
}

func (f *fakeLeagueStore) GetLatestEventID(_ context.Context, _ int64) (int, error) {
	return f.latestID, f.err
}

type fakeFPLQuerier struct {
	bootstrap fpl.BootstrapResponse
	err       error
}

func (f *fakeFPLQuerier) GetBootstrap(_ context.Context) (fpl.BootstrapResponse, error) {
	return f.bootstrap, f.err
}

type fakePollerStatus struct {
	state     string
	lastEvent int
}

func (f *fakePollerStatus) Status() (string, int) {
	return f.state, f.lastEvent
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestHandler(tg *fakeTelegramBot, sq *fakeStatsQuerier, ls *fakeLeagueStore, fq *fakeFPLQuerier, ps *fakePollerStatus) *Handler {
	return New(tg, sq, ls, fq, ps, Config{
		LeagueID:       916670,
		ChatID:         "-12345",
		Port:           0,
		WebhookBaseURL: "https://example.com",
		WebhookSecret:  "test-secret",
	})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDispatchCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		store   *fakeLeagueStore
		stats   *fakeStatsQuerier
		fpl     *fakeFPLQuerier
		wantErr bool
		wantMsg string // substring expected in the response
	}{
		{
			name:    "standings with data",
			command: "/standings",
			store: &fakeLeagueStore{
				latestID: 5,
				standings: []store.GameweekStanding{
					{LeagueID: 916670, EventID: 5, ManagerID: 101, Rank: 1, Points: 12, TotalScore: 350},
					{LeagueID: 916670, EventID: 5, ManagerID: 202, Rank: 2, Points: 9, TotalScore: 320},
				},
				managers: []store.Manager{
					{LeagueID: 916670, ID: 101, Name: "Alice", TeamName: "Alice FC"},
					{LeagueID: 916670, ID: 202, Name: "Bob", TeamName: "Bob FC"},
				},
			},
			stats:   &fakeStatsQuerier{},
			fpl:     &fakeFPLQuerier{},
			wantMsg: "Standings after GW5",
		},
		{
			name:    "standings no data",
			command: "/standings",
			store:   &fakeLeagueStore{latestID: 0},
			stats:   &fakeStatsQuerier{},
			fpl:     &fakeFPLQuerier{},
			wantMsg: "No gameweek data",
		},
		{
			name:    "streak with data",
			command: "/streak",
			store:   &fakeLeagueStore{},
			stats: &fakeStatsQuerier{
				streaks: []stats.CurrentStreak{
					{
						Manager:    notify.ManagerRef{ID: 101, Name: "Alice", TeamName: "Alice FC"},
						Kind:       notify.StreakKindWin,
						Length:     3,
						StartedAt:  3,
						FinishedAt: 5,
					},
				},
			},
			fpl:     &fakeFPLQuerier{},
			wantMsg: "Active Streaks",
		},
		{
			name:    "streak no data",
			command: "/streak",
			store:   &fakeLeagueStore{},
			stats:   &fakeStatsQuerier{streaks: nil},
			fpl:     &fakeFPLQuerier{},
			wantMsg: "No streaks yet",
		},
		{
			name:    "deadline with next event",
			command: "/deadline",
			store:   &fakeLeagueStore{},
			stats:   &fakeStatsQuerier{},
			fpl: &fakeFPLQuerier{
				bootstrap: fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 1, Name: "Gameweek 1", IsNext: false},
						{ID: 2, Name: "Gameweek 2", IsNext: true, DeadlineTime: "2026-01-15T11:30:00Z"},
					},
				},
			},
			wantMsg: "Gameweek 2",
		},
		{
			name:    "deadline no next event",
			command: "/deadline",
			store:   &fakeLeagueStore{},
			stats:   &fakeStatsQuerier{},
			fpl: &fakeFPLQuerier{
				bootstrap: fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 38, Name: "Gameweek 38", IsNext: false, Finished: true},
					},
				},
			},
			wantMsg: "No upcoming deadline",
		},
		{
			name:    "standings error propagates",
			command: "/standings",
			store:   &fakeLeagueStore{latestID: 5, err: fmt.Errorf("db connection lost")},
			stats:   &fakeStatsQuerier{},
			fpl:     &fakeFPLQuerier{},
			wantErr: true,
		},
		{
			name:    "unknown command returns empty",
			command: "/unknown",
			store:   &fakeLeagueStore{},
			stats:   &fakeStatsQuerier{},
			fpl:     &fakeFPLQuerier{},
			wantMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tg := &fakeTelegramBot{}
			h := newTestHandler(tg, tt.stats, tt.store, tt.fpl, &fakePollerStatus{})

			response, err := h.dispatchCommand(context.Background(), tt.command, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantMsg != "" && !strings.Contains(response, tt.wantMsg) {
				t.Errorf("response = %q, want substring %q", response, tt.wantMsg)
			}
			if tt.wantMsg == "" && response != "" {
				t.Errorf("expected empty response, got %q", response)
			}
		})
	}
}

func TestHandleUpdate_ChatIDFiltering(t *testing.T) {
	tg := &fakeTelegramBot{}
	h := newTestHandler(tg, &fakeStatsQuerier{streaks: nil}, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	// Send an update from a different chat ID.
	h.handleUpdate(context.Background(), update{
		UpdateID: 1,
		Message: &message{
			Text: "/streak",
			Chat: chat{ID: -99999}, // not -12345
		},
	})

	if len(tg.sentMessages()) != 0 {
		t.Errorf("expected zero sends for unauthorized chat, got %d", len(tg.sentMessages()))
	}
}

func TestHandleUpdate_UnknownCommand(t *testing.T) {
	tg := &fakeTelegramBot{}
	h := newTestHandler(tg, &fakeStatsQuerier{}, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	h.handleUpdate(context.Background(), update{
		UpdateID: 1,
		Message: &message{
			Text: "/doesnotexist",
			Chat: chat{ID: -12345},
		},
	})

	if len(tg.sentMessages()) != 0 {
		t.Errorf("expected zero sends for unknown command, got %d", len(tg.sentMessages()))
	}
}

func TestHandleUpdate_NotACommand(t *testing.T) {
	tg := &fakeTelegramBot{}
	h := newTestHandler(tg, &fakeStatsQuerier{}, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	h.handleUpdate(context.Background(), update{
		UpdateID: 1,
		Message: &message{
			Text: "just a regular chat message",
			Chat: chat{ID: -12345},
		},
	})

	if len(tg.sentMessages()) != 0 {
		t.Errorf("expected zero sends for non-command, got %d", len(tg.sentMessages()))
	}
}

func TestHandleUpdate_NilMessage(t *testing.T) {
	tg := &fakeTelegramBot{}
	h := newTestHandler(tg, &fakeStatsQuerier{}, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	h.handleUpdate(context.Background(), update{
		UpdateID: 1,
		Message:  nil,
	})

	if len(tg.sentMessages()) != 0 {
		t.Errorf("expected zero sends for nil message, got %d", len(tg.sentMessages()))
	}
}

func TestHandleUpdate_StripsBotUsername(t *testing.T) {
	tg := &fakeTelegramBot{}
	sq := &fakeStatsQuerier{streaks: nil}
	h := newTestHandler(tg, sq, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	h.handleUpdate(context.Background(), update{
		UpdateID: 1,
		Message: &message{
			Text: "/streak@MyBanterBot",
			Chat: chat{ID: -12345},
		},
	})

	// The command should still be dispatched — the @BotName suffix is stripped.
	sent := tg.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sent))
	}
	if !strings.Contains(sent[0].text, "No streaks yet") {
		t.Errorf("response = %q, want 'No streaks yet'", sent[0].text)
	}
}

func TestResolveManager_ByRank(t *testing.T) {
	managers := []store.Manager{
		{LeagueID: 916670, ID: 101, Name: "Alice"},
		{LeagueID: 916670, ID: 202, Name: "Bob"},
	}
	standings := []store.GameweekStanding{
		{ManagerID: 101, Rank: 1},
		{ManagerID: 202, Rank: 2},
	}

	m, err := resolveManager("1", managers, standings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID != 101 {
		t.Errorf("resolved manager ID = %d, want 101", m.ID)
	}
}

func TestResolveManager_ByRank_NotFound(t *testing.T) {
	managers := []store.Manager{
		{LeagueID: 916670, ID: 101, Name: "Alice"},
	}
	standings := []store.GameweekStanding{
		{ManagerID: 101, Rank: 1},
	}

	_, err := resolveManager("99", managers, standings)
	if err == nil {
		t.Fatal("expected error for non-existent rank")
	}
	if !strings.Contains(err.Error(), "no manager at rank 99") {
		t.Errorf("error = %q, want 'no manager at rank 99'", err.Error())
	}
}

func TestResolveManager_ByName(t *testing.T) {
	managers := []store.Manager{
		{LeagueID: 916670, ID: 101, Name: "Alice"},
		{LeagueID: 916670, ID: 202, Name: "Bob"},
	}
	standings := []store.GameweekStanding{
		{ManagerID: 101, Rank: 1},
		{ManagerID: 202, Rank: 2},
	}

	m, err := resolveManager("ali", managers, standings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID != 101 {
		t.Errorf("resolved manager ID = %d, want 101", m.ID)
	}
}

func TestResolveManager_ByName_Ambiguous(t *testing.T) {
	managers := []store.Manager{
		{LeagueID: 916670, ID: 101, Name: "Alice Adams"},
		{LeagueID: 916670, ID: 202, Name: "Alice Baker"},
	}
	standings := []store.GameweekStanding{}

	_, err := resolveManager("ali", managers, standings)
	if err == nil {
		t.Fatal("expected error for ambiguous name")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error = %q, want 'ambiguous'", err.Error())
	}
}

func TestResolveManager_ByName_NotFound(t *testing.T) {
	managers := []store.Manager{
		{LeagueID: 916670, ID: 101, Name: "Alice"},
	}
	standings := []store.GameweekStanding{}

	_, err := resolveManager("xyz", managers, standings)
	if err == nil {
		t.Fatal("expected error for non-matching name")
	}
	if !strings.Contains(err.Error(), "no manager matching") {
		t.Errorf("error = %q, want 'no manager matching'", err.Error())
	}
}

func TestHandleHistory_SameManager(t *testing.T) {
	tg := &fakeTelegramBot{}
	sq := &fakeStatsQuerier{}
	ls := &fakeLeagueStore{
		latestID: 5,
		standings: []store.GameweekStanding{
			{ManagerID: 101, Rank: 1},
			{ManagerID: 202, Rank: 2},
		},
		managers: []store.Manager{
			{LeagueID: 916670, ID: 101, Name: "Alice"},
			{LeagueID: 916670, ID: 202, Name: "Bob"},
		},
	}
	h := newTestHandler(tg, sq, ls, &fakeFPLQuerier{}, &fakePollerStatus{})

	response, err := h.dispatchCommand(context.Background(), "/history", []string{"1", "1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response != "That's the same manager!" {
		t.Errorf("response = %q, want 'That's the same manager!'", response)
	}
}

func TestHandleHistory_InsufficientArgs(t *testing.T) {
	tg := &fakeTelegramBot{}
	h := newTestHandler(tg, &fakeStatsQuerier{}, &fakeLeagueStore{}, &fakeFPLQuerier{}, &fakePollerStatus{})

	response, err := h.dispatchCommand(context.Background(), "/history", []string{"1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(response, "Usage:") {
		t.Errorf("response = %q, want usage message", response)
	}
}

func TestServeHealth(t *testing.T) {
	ps := &fakePollerStatus{state: "idle", lastEvent: 25}
	h := newTestHandler(&fakeTelegramBot{}, &fakeStatsQuerier{}, &fakeLeagueStore{}, &fakeFPLQuerier{}, ps)

	// Use httptest to call the handler directly.
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.serveHealth(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if result["status"] != "ok" {
		t.Errorf("status = %q, want 'ok'", result["status"])
	}
	if result["poller_state"] != "idle" {
		t.Errorf("poller_state = %q, want 'idle'", result["poller_state"])
	}
	if result["last_processed_event"] != float64(25) {
		t.Errorf("last_processed_event = %v, want 25", result["last_processed_event"])
	}
}

func TestHandleUpdate_CommandErrorReturnsGenericMessage(t *testing.T) {
	tg := &fakeTelegramBot{}
	ls := &fakeLeagueStore{
		latestID: 5,
		err:      fmt.Errorf("database on fire"),
	}
	h := newTestHandler(tg, &fakeStatsQuerier{}, ls, &fakeFPLQuerier{}, &fakePollerStatus{})

	h.handleUpdate(context.Background(), update{
		UpdateID: 1,
		Message: &message{
			Text: "/standings",
			Chat: chat{ID: -12345},
		},
	})

	sent := tg.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sent))
	}
	if !strings.Contains(sent[0].text, "something went wrong") {
		t.Errorf("response = %q, want generic error message", sent[0].text)
	}
}

func TestFormatDeadline(t *testing.T) {
	// Parse a known deadline and verify it formats in London time.
	deadline, _ := time.Parse(time.RFC3339, "2026-01-15T11:30:00Z")
	result := formatDeadline("Gameweek 21", deadline)

	if !strings.Contains(result, "Gameweek 21") {
		t.Errorf("result = %q, want 'Gameweek 21'", result)
	}
	// In winter, London is UTC (GMT), so 11:30 UTC = 11:30 GMT.
	if !strings.Contains(result, "11:30") {
		t.Errorf("result = %q, want '11:30'", result)
	}
}

func TestFormatStandings(t *testing.T) {
	standings := []store.GameweekStanding{
		{LeagueID: 916670, EventID: 5, ManagerID: 101, Rank: 1, Points: 12, TotalScore: 350},
		{LeagueID: 916670, EventID: 5, ManagerID: 202, Rank: 2, Points: 9, TotalScore: 320},
	}
	managers := []store.Manager{
		{LeagueID: 916670, ID: 101, Name: "Alice"},
		{LeagueID: 916670, ID: 202, Name: "Bob"},
	}

	result := formatStandings(5, standings, managers)

	if !strings.Contains(result, "Standings after GW5") {
		t.Errorf("result = %q, want header", result)
	}
	if !strings.Contains(result, "Alice") || !strings.Contains(result, "Bob") {
		t.Errorf("result = %q, want manager names", result)
	}
}

func TestFormatH2HRecord(t *testing.T) {
	record := stats.H2HRecord{
		LeagueID:      916670,
		ManagerA:      notify.ManagerRef{ID: 101, Name: "Alice"},
		ManagerB:      notify.ManagerRef{ID: 202, Name: "Bob"},
		GamesPlayed:   5,
		ManagerAWins:  3,
		ManagerBWins:  1,
		Draws:         1,
		ManagerAScore: 350,
		ManagerBScore: 300,
	}

	result := formatH2HRecord(record)

	if !strings.Contains(result, "Alice vs Bob") {
		t.Errorf("result = %q, want 'Alice vs Bob'", result)
	}
	if !strings.Contains(result, "Played: 5") {
		t.Errorf("result = %q, want 'Played: 5'", result)
	}
}

func TestFormatH2HRecord_NoGames(t *testing.T) {
	record := stats.H2HRecord{
		ManagerA: notify.ManagerRef{ID: 101, Name: "Alice"},
		ManagerB: notify.ManagerRef{ID: 202, Name: "Bob"},
	}

	result := formatH2HRecord(record)

	if !strings.Contains(result, "haven't played") {
		t.Errorf("result = %q, want 'haven't played'", result)
	}
}

func TestOrdinal(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{1, "1st"}, {2, "2nd"}, {3, "3rd"}, {4, "4th"},
		{11, "11th"}, {12, "12th"}, {13, "13th"},
		{21, "21st"}, {22, "22nd"}, {23, "23rd"},
		{101, "101st"}, {111, "111th"}, {112, "112th"},
	}

	for _, tt := range tests {
		if got := ordinal(tt.n); got != tt.want {
			t.Errorf("ordinal(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// TestCommands_MatchesDispatch verifies that every entry in the Commands
// metadata slice has a corresponding case in dispatchCommand. This helps
// prevent the two from drifting — if someone adds a command constant and
// an entry in Commands but forgets the switch case, this test catches it.
func TestCommands_MatchesDispatch(t *testing.T) {
	// Build a set of command names from the exported metadata.
	registered := make(map[string]bool, len(Commands))
	for _, cmd := range Commands {
		registered[cmd.Name] = true
	}

	// Set up a handler with fakes that return valid data for all commands.
	// We need real-ish data so handlers don't error out — we just care
	// that they're reached (non-empty response).
	tg := &fakeTelegramBot{}
	ls := &fakeLeagueStore{
		latestID:  1,
		standings: []store.GameweekStanding{{ManagerID: 1, Rank: 1}, {ManagerID: 2, Rank: 2}},
		managers:  []store.Manager{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}},
	}
	sq := &fakeStatsQuerier{streaks: nil}
	fq := &fakeFPLQuerier{bootstrap: fpl.BootstrapResponse{
		Events: []fpl.Event{{ID: 1, Name: "GW1", IsNext: true, DeadlineTime: "2026-01-15T11:30:00Z"}},
	}}
	h := newTestHandler(tg, sq, ls, fq, &fakePollerStatus{})

	// For each registered command, dispatch it and verify it's handled
	// (non-empty response = the switch case exists and the handler ran).
	for _, cmd := range Commands {
		t.Run(cmd.Name, func(t *testing.T) {
			// /history needs args; other commands don't.
			var args []string
			if cmd.Name == CmdHistory {
				args = []string{"1", "2"}
			}

			response, err := h.dispatchCommand(context.Background(), "/"+cmd.Name, args)
			if err != nil {
				t.Fatalf("dispatch error for /%s: %v", cmd.Name, err)
			}
			if response == "" {
				t.Errorf("/%s returned empty response — missing case in dispatchCommand?", cmd.Name)
			}
		})
	}
}
