package main

import (
	"context"
	"sync"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/stats"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

type bootstrapFetcher interface {
	GetBootstrap(ctx context.Context) (fpl.BootstrapResponse, error)
}

func newPlayerLookup(fetcher bootstrapFetcher) stats.PlayerLookupFunc {
	var (
		mu          sync.Mutex
		playerNames map[int]notify.PlayerRef
	)

	return stats.PlayerLookupFunc(func(ctx context.Context) (map[int]notify.PlayerRef, error) {
		mu.Lock()
		if playerNames != nil {
			defer mu.Unlock()
			return playerNames, nil
		}
		mu.Unlock()

		bootstrap, err := fetcher.GetBootstrap(ctx)
		if err != nil {
			return nil, err
		}

		names := make(map[int]notify.PlayerRef, len(bootstrap.Elements))
		for _, element := range bootstrap.Elements {
			names[element.ID] = notify.PlayerRef{
				ElementID: element.ID,
				Name:      element.WebName,
			}
		}

		mu.Lock()
		if playerNames == nil {
			playerNames = names
		}
		cached := playerNames
		mu.Unlock()

		return cached, nil
	})
}
