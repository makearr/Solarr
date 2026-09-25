package music_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/media/music"
)

const losslessFirstRules = `{"quality_profile":"lossless-first","compare":["audio_codec","score"],` +
	`"tiers":{"audio_codec":[["flac"],["mp3"]]},"targets":{}}`

// Lidarr's automatic search sends the best release, whatever order the indexer listed them in.
func TestGrab_SendsTheBestReleaseAndNeverSearchesAnAlbumAlreadyDownloading(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{
			rawItem("guid-mp3", "Nosi Zada-Discovery-2001-MP3-320-GROUP"),
			rawItem("guid-flac", "Nosi Zada-Discovery-2001-FLAC-GROUP"),
		}, nil
	}}
	sender := &fakeSabnzbdSender{clientRef: "nzo-1"}
	grab := music.Grab{Search: env.search(), Dispatch: env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "usenet", Client: sender},
	})}
	indexers := []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}

	first, err := grab.SearchAndSend(t.Context(), item.ID, indexers, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchAndSend: %v", err)
	}
	if first.Sent == nil || first.Sent.CandidateTitle != "Nosi Zada-Discovery-2001-FLAC-GROUP" {
		t.Fatalf("sent %+v, want the FLAC release", first.Sent)
	}

	calls := len(searcher.requests)
	second, err := grab.SearchAndSend(context.Background(), item.ID, indexers, env.clock.Now())
	if err != nil {
		t.Fatalf("second SearchAndSend: %v", err)
	}
	if !reflect.DeepEqual(second, music.GrabResult{AlreadyInFlight: true}) || len(searcher.requests) != calls {
		t.Errorf("second = %+v after %d more indexer calls; want AlreadyInFlight and no search",
			second, len(searcher.requests)-calls)
	}
}
