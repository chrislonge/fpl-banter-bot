package main

import (
	"context"
	"errors"
	"testing"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
)

type stubBootstrapFetcher struct {
	calls     int
	responses []bootstrapResult
}

type bootstrapResult struct {
	resp fpl.BootstrapResponse
	err  error
}

func (s *stubBootstrapFetcher) GetBootstrap(_ context.Context) (fpl.BootstrapResponse, error) {
	s.calls++
	if len(s.responses) == 0 {
		return fpl.BootstrapResponse{}, nil
	}
	result := s.responses[0]
	if len(s.responses) > 1 {
		s.responses = s.responses[1:]
	}
	return result.resp, result.err
}

func TestNewPlayerLookup_RetriesAfterFailure(t *testing.T) {
	fetcher := &stubBootstrapFetcher{
		responses: []bootstrapResult{
			{err: errors.New("temporary outage")},
			{resp: fpl.BootstrapResponse{Elements: []fpl.Element{{ID: 11, WebName: "Salah"}}}},
		},
	}

	lookup := newPlayerLookup(fetcher)

	if _, err := lookup(context.Background()); err == nil {
		t.Fatal("expected first lookup to fail")
	}

	names, err := lookup(context.Background())
	if err != nil {
		t.Fatalf("second lookup failed: %v", err)
	}
	if fetcher.calls != 2 {
		t.Fatalf("bootstrap calls = %d, want 2", fetcher.calls)
	}
	if got := names[11].Name; got != "Salah" {
		t.Fatalf("player name = %q, want %q", got, "Salah")
	}
}

func TestNewPlayerLookup_CachesSuccessfulResponse(t *testing.T) {
	fetcher := &stubBootstrapFetcher{
		responses: []bootstrapResult{
			{resp: fpl.BootstrapResponse{Elements: []fpl.Element{{ID: 4, WebName: "van Dijk"}}}},
		},
	}

	lookup := newPlayerLookup(fetcher)

	first, err := lookup(context.Background())
	if err != nil {
		t.Fatalf("first lookup failed: %v", err)
	}
	second, err := lookup(context.Background())
	if err != nil {
		t.Fatalf("second lookup failed: %v", err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("bootstrap calls = %d, want 1", fetcher.calls)
	}
	if first[4].Name != second[4].Name {
		t.Fatalf("cached name mismatch: %q vs %q", first[4].Name, second[4].Name)
	}
}
