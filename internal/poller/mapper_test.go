package poller

import (
	"testing"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
)

func TestMapH2HResults(t *testing.T) {
	tests := []struct {
		name         string
		eventID      int
		matches      []fpl.H2HMatch
		wantCount    int
		wantEventID  int
		wantManager1 int64
		wantScore1   int
		wantManager2 int64
		wantScore2   int
	}{
		{
			eventID: 5,
			name:    "preserves canonical ordering",
			matches: []fpl.H2HMatch{
				{Event: 5, Entry1Entry: 100, Entry1Points: 60, Entry2Entry: 200, Entry2Points: 55},
			},
			wantCount:    1,
			wantEventID:  5,
			wantManager1: 100,
			wantScore1:   60,
			wantManager2: 200,
			wantScore2:   55,
		},
		{
			eventID: 5,
			name:    "reorders IDs and scores when API order is reversed",
			matches: []fpl.H2HMatch{
				{Event: 5, Entry1Entry: 200, Entry1Points: 55, Entry2Entry: 100, Entry2Points: 60},
			},
			wantCount:    1,
			wantEventID:  5,
			wantManager1: 100,
			wantScore1:   60,
			wantManager2: 200,
			wantScore2:   55,
		},
		{
			eventID: 5,
			name:    "uses caller event even when payload event disagrees",
			matches: []fpl.H2HMatch{
				{Event: 99, Entry1Entry: 100, Entry1Points: 60, Entry2Entry: 200, Entry2Points: 55},
			},
			wantCount:    1,
			wantEventID:  5,
			wantManager1: 100,
			wantScore1:   60,
			wantManager2: 200,
			wantScore2:   55,
		},
		{
			eventID: 5,
			name:    "skips bye rows",
			matches: []fpl.H2HMatch{
				{Event: 5, Entry1Entry: 100, Entry1Points: 60, Entry2Entry: 0, Entry2Points: 0, IsBye: true},
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapH2HResults(42, tt.eventID, tt.matches)
			if len(got) != tt.wantCount {
				t.Fatalf("len(results) = %d, want %d", len(got), tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}

			result := got[0]
			if result.Manager1ID != tt.wantManager1 || result.Manager2ID != tt.wantManager2 {
				t.Fatalf("manager IDs = %d/%d, want %d/%d", result.Manager1ID, result.Manager2ID, tt.wantManager1, tt.wantManager2)
			}
			if result.Manager1Score != tt.wantScore1 || result.Manager2Score != tt.wantScore2 {
				t.Fatalf("scores = %d/%d, want %d/%d", result.Manager1Score, result.Manager2Score, tt.wantScore1, tt.wantScore2)
			}
			if result.LeagueID != 42 || result.EventID != tt.wantEventID {
				t.Fatalf("league/event = %d/%d, want 42/%d", result.LeagueID, result.EventID, tt.wantEventID)
			}
		})
	}
}
