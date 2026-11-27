package adapters

import (
	"context"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
)

// previewSetup counts what importSetup and importLists would add, reading the *Arr and changing nothing; a read that
// fails is said in Problem so the rest of the preview still shows.
func (a ArrSync) previewSetup(
	ctx context.Context, instance settings.ArrInstance, client arrimport.LidarrClient, options apiserver.ArrSyncOptions,
) *apiserver.ArrPreviewSetup {
	if (!options.Indexers && !options.DownloadClients && !options.ImportLists) || a.Setup.Cache == nil || instance.App.ComicManager() {
		return nil
	}
	setup := &apiserver.ArrPreviewSetup{}
	var problems []string
	if options.Indexers {
		found, err := arrIndexers(ctx, instance.App, client)
		if err != nil {
			problems = append(problems, "indexers: "+err.Error())
		} else {
			setup.Indexers, setup.IndexersWithoutKey = a.Setup.countNewIndexers(ctx, found)
		}
	}
	if options.DownloadClients {
		found, err := arrDownloadClients(ctx, instance.App, client)
		if err != nil {
			problems = append(problems, "download clients: "+err.Error())
		} else {
			setup.DownloadClients = a.Setup.countNewDownloadClients(ctx, found)
		}
	}
	if options.ImportLists {
		found, err := arrImportLists(ctx, instance.App, client)
		if err != nil {
			problems = append(problems, "import lists: "+err.Error())
		}
		for _, entry := range found {
			if _, _, _, mapped := mapArrImportList(entry); mapped {
				setup.ImportLists++
			}
		}
	}
	setup.Problem = strings.Join(problems, "; ")
	return setup
}

// countNewIndexers is how many of the *Arr's usable indexers are not here yet, and how many of those arrive without a key.
func (s ArrSetup) countNewIndexers(ctx context.Context, found []arrimport.Indexer) (added, withoutKey int64) {
	have, err := s.Cache.Indexers(ctx)
	if err != nil {
		return 0, 0
	}
	known := map[string]bool{}
	for _, existing := range have {
		known[strings.TrimRight(existing.BaseURL, "/")] = true
	}
	prowlarr := s.prowlarrKeys(ctx)
	for _, entry := range found {
		impl := strings.ToLower(entry.Implementation)
		if (impl != "newznab" && impl != "torznab") || entry.BaseURL == "" || known[strings.TrimRight(entry.BaseURL, "/")] {
			continue
		}
		added++
		if arrimport.Masked(entry.APIKey) || entry.APIKey == "" {
			if _, viaProwlarr := prowlarr[hostOf(entry.BaseURL)]; !viaProwlarr {
				withoutKey++
			}
		}
	}
	return added, withoutKey
}

// countNewDownloadClients is how many of the *Arr's clients Solarr can use are not here yet.
func (s ArrSetup) countNewDownloadClients(ctx context.Context, found []arrimport.DownloadClient) int64 {
	have, err := s.Cache.Downloaders(ctx)
	if err != nil {
		return 0
	}
	known := map[string]bool{}
	for _, existing := range have {
		known[strings.TrimRight(existing.BaseURL, "/")] = true
	}
	var added int64
	for _, entry := range found {
		if _, supported := DownloadClientDefinition(strings.ToLower(entry.Implementation)); supported && !known[entry.BaseURL()] {
			added++
		}
	}
	return added
}
