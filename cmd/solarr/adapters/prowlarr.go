package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// Prowlarr satisfies apiserver.ProwlarrLister: Prowlarr's indexers, marked when already added here.
type Prowlarr struct {
	Cache *settings.Cache
	List  func(ctx context.Context, baseURL, apiKey string) ([]indexer.ProwlarrIndexer, error)
}

func (a Prowlarr) ListProwlarr(ctx context.Context, baseURL, apiKey string) ([]apiserver.ProwlarrIndexerRow, error) {
	listed, err := a.List(ctx, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	configured, err := a.Cache.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	added := make(map[string]bool, len(configured))
	for _, existing := range configured {
		added[existing.BaseURL] = true
	}
	rows := make([]apiserver.ProwlarrIndexerRow, len(listed))
	for i, entry := range listed {
		rows[i] = apiserver.ProwlarrIndexerRow{
			ProwlarrID: entry.ProwlarrID, Name: entry.Name, Impl: entry.Impl, BaseURL: entry.BaseURL,
			Enabled: entry.Enabled, Music: entry.Music, AlreadyAdded: added[entry.BaseURL],
		}
	}
	return rows, nil
}
