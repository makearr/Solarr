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

	first, err := grab.SearchAndSend(t.Context(), item.ID, indexers, music.GrabAutomatic, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchAndSend: %v", err)
	}
	if first.Sent == nil || first.Sent.CandidateTitle != "Nosi Zada-Discovery-2001-FLAC-GROUP" {
		t.Fatalf("sent %+v, want the FLAC release", first.Sent)
	}

	calls := len(searcher.requests)
	second, err := grab.SearchAndSend(context.Background(), item.ID, indexers, music.GrabAutomatic, env.clock.Now())
	if err != nil {
		t.Fatalf("second SearchAndSend: %v", err)
	}
	if !reflect.DeepEqual(second, music.GrabResult{AlreadyInFlight: true}) || len(searcher.requests) != calls {
		t.Errorf("second = %+v after %d more indexer calls; want AlreadyInFlight and no search",
			second, len(searcher.requests)-calls)
	}
}

// A torrent ranked best is passed over while no torrent client exists; the best usenet release goes.
func TestGrab_PassesOverAReleaseNoClientCanTake(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	torrents := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{rawItem("guid-t", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
	}}
	usenet := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{rawItem("guid-u", "Nosi Zada-Discovery-2001-MP3-320-GROUP")}, nil
	}}
	sender := &fakeSabnzbdSender{clientRef: "nzo-1"}
	grab := music.Grab{Search: env.search(), Dispatch: env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: sender}})}

	result, err := grab.SearchAndSend(t.Context(), item.ID, []music.IndexerConn{
		{ID: 1, Impl: "torznab", Client: torrents}, {ID: 2, Impl: "newznab", Client: usenet},
	}, music.GrabAutomatic, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchAndSend: %v", err)
	}
	if result.Sent == nil || result.Sent.CandidateTitle != "Nosi Zada-Discovery-2001-MP3-320-GROUP" {
		t.Errorf("sent %+v, want the usenet release", result.Sent)
	}
}

// A client that declines the best release (SABnzbd's duplicate detection, found live 2026-09-29)
// gets the next best, as Lidarr does, and the refusal is counted rather than reported as a failure.
func TestGrab_ARefusedReleaseGivesWayToTheNextBest(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{
			rawItem("guid-flac", "Nosi Zada-Discovery-2001-FLAC-GROUP"),
			rawItem("guid-mp3", "Nosi Zada-Discovery-2001-MP3-320-GROUP"),
		}, nil
	}}
	sender := &fakeSabnzbdSender{clientRef: "nzo-1", refuse: map[string]bool{"Nosi Zada-Discovery-2001-FLAC-GROUP": true}}
	grab := music.Grab{Search: env.search(), Dispatch: env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: sender}})}

	result, err := grab.SearchAndSend(t.Context(), item.ID, []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, music.GrabAutomatic, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchAndSend: %v", err)
	}
	if result.Sent == nil || result.Sent.CandidateTitle != "Nosi Zada-Discovery-2001-MP3-320-GROUP" || result.Refused != 1 {
		t.Errorf("sent %+v, refused %d; want the MP3 release after one refusal", result.Sent, result.Refused)
	}
}
