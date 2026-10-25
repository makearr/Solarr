package adapters_test

import (
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

// Every catalog entry is unique, has a name and builds; its companion, if any, is a download client that names it back.
func TestIndexerDefinitions_EveryEntryBuildsAndCompanionsPair(t *testing.T) {
	seen := map[string]bool{}
	privacy := map[indexer.Privacy]int{}
	for _, definition := range adapters.IndexerDefinitions() {
		if seen[definition.ID] || definition.Name == "" || len(definition.MediaTypes) == 0 {
			t.Errorf("%s: duplicate, unnamed or for no media", definition.ID)
		}
		seen[definition.ID] = true
		privacy[definition.Privacy]++
		if driver, known := adapters.NewIndexer(definition.Impl, adapters.IndexerDeps{}, indexer.Connection{BaseURL: "https://x"}); !known || driver == nil {
			t.Errorf("%s: impl %q has no implementation", definition.ID, definition.Impl)
		}
		if definition.CompanionDownloader != "" {
			client, known := adapters.DownloadClientDefinition(definition.CompanionDownloader)
			if !known || client.CompanionIndexer != definition.ID || string(client.Protocol) != definition.Protocol() {
				t.Errorf("%s: companion %q does not pair back (%+v)", definition.ID, definition.CompanionDownloader, client)
			}
		}
	}
	if privacy[indexer.PrivacyPublic] < 5 || privacy[indexer.PrivacyPrivate] < 20 || !seen["soulseek"] {
		t.Errorf("catalog = %d entries, by privacy %v", len(seen), privacy)
	}
}
