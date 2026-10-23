package music_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
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
	if !reflect.DeepEqual(second, music.GrabResult{Best: dispatch.Best{AlreadyInFlight: true}}) || len(searcher.requests) != calls {
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
	// Why it was refused is kept, so "found but nothing sent" is never unexplained (2026-09-30).
	if len(result.Refusals) != 1 || !strings.HasPrefix(result.Refusals[0], "Nosi Zada-Discovery-2001-FLAC-GROUP: ") {
		t.Errorf("Refusals = %q, want the FLAC release named with its reason", result.Refusals)
	}
}

// Section 10.11: a release whose content failed before is refused under any listing, and the
// next best goes instead.
func TestGrab_ContentThatFailedBeforeIsRefusedUnderANewListing(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{
			rawItem("guid-flac", "Nosi Zada-Discovery-2001-FLAC-GROUP"),
			rawItem("guid-mp3", "Nosi Zada-Discovery-2001-MP3-320-GROUP"),
		}, nil
	}}
	sender := &fakeSabnzbdSender{clientRef: "nzo-1"}
	dispatch := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: sender}})
	failed := []byte(`<nzb><segment bytes="1" number="1">failed@x</segment></nzb>`)
	dispatch.Fetch = func(_ context.Context, _ int64, link string) ([]byte, error) {
		if strings.Contains(link, "guid-flac") {
			return failed, nil
		}
		return []byte(`<nzb><segment bytes="1" number="1">fine@x</segment></nzb>`), nil
	}
	blocklist := library.NewBlocklistStore(env.library)
	dispatch.Blocklist = &blocklist
	if _, err := blocklist.Create(t.Context(), library.NewBlocklistEntry{
		ContentHash: download.NZBContentHash(failed), Title: "the same upload on another indexer",
		Reason: library.BlocklistDownloadFailed, Source: library.BlocklistSourceAuto, CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}

	grab := music.Grab{Search: env.search(), Dispatch: dispatch}
	result, err := grab.SearchAndSend(t.Context(), item.ID, []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}},
		music.GrabAutomatic, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchAndSend: %v", err)
	}
	if result.Sent == nil || result.Sent.CandidateTitle != "Nosi Zada-Discovery-2001-MP3-320-GROUP" {
		t.Fatalf("sent %+v, want the MP3 release", result.Sent)
	}
	if want := download.NZBContentHash([]byte(`<segment bytes="1" number="1">fine@x</segment>`)); result.Sent.ContentHash != want {
		t.Errorf("sent content hash %q, want %q recorded for a later failure", result.Sent.ContentHash, want)
	}
}

// ADR-12: an automatic grab never fills a library past its reserve; a person's own grab still goes.
func TestGrab_AnAutomaticGrabLeavesTheReserveAlone(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	root, err := env.settings.CreateRootFolder(t.Context(), "/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if err := env.settings.AttachRootFolder(t.Context(), settings.CategoryRoot{
		CategoryID: item.CategoryID, RootFolderID: root.ID, Weight: 1, AcceptsNew: true,
	}); err != nil {
		t.Fatalf("AttachRootFolder: %v", err)
	}
	// 100 MB above the reserve; every release below is 500 MB.
	if err := env.settings.SetRootFolderCapacity(t.Context(), root.ID, settings.RootFolderCapacity{
		Writable: true, TotalBytes: 1_000_000_000_000, FreeBytes: settings.DefaultMinFreeBytes + 100_000_000, CheckedAt: 1,
	}); err != nil {
		t.Fatalf("SetRootFolderCapacity: %v", err)
	}
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{rawItem("guid-flac", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
	}}
	sender := &fakeSabnzbdSender{clientRef: "nzo-1"}
	grab := music.Grab{Search: env.search(), Dispatch: env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "usenet", Client: sender, AlwaysSendUser: true},
	})}
	indexers := []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}

	automatic, err := grab.SearchAndSend(t.Context(), item.ID, indexers, music.GrabAutomatic, env.clock.Now())
	if err != nil || automatic.Sent != nil || automatic.NoRoom != 1 {
		t.Fatalf("automatic = sent %v, no room %d, %v; want the release passed over for room", automatic.Sent, automatic.NoRoom, err)
	}
	own, err := grab.SearchAndSend(t.Context(), item.ID, indexers, music.GrabUser, env.clock.Now())
	if err != nil || own.Sent == nil {
		t.Fatalf("own grab = %+v, %v; want it sent: a person's grab is never gated", own, err)
	}
}

// Free space is measured every 6 hours, so what is already downloading is subtracted first: two
// automatic grabs of 500 MB never both go into 700 MB above the reserve (2026-09-30).
func TestGrab_WhatIsAlreadyDownloadingCountsAgainstTheReserve(t *testing.T) {
	env := newSearchTestEnv(t)
	first := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	secondWork := env.createWork(t, "Homework", 1997)
	people := library.NewPeople(env.library)
	personID, err := people.ResolvePerson(t.Context(), env.clock.Now(), library.PersonLink{
		Provider: "test", ExternalID: "Nosi Zada", Person: library.PersonIdentity{Name: "Nosi Zada"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddLeadCredit(t.Context(), secondWork, personID, "artist"); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}
	second, err := env.desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: secondWork, CategoryID: first.CategoryID, ProfileID: first.ProfileID, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}
	root, err := env.settings.CreateRootFolder(t.Context(), "/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if err := env.settings.AttachRootFolder(t.Context(), settings.CategoryRoot{
		CategoryID: first.CategoryID, RootFolderID: root.ID, Weight: 1, AcceptsNew: true,
	}); err != nil {
		t.Fatalf("AttachRootFolder: %v", err)
	}
	if err := env.settings.SetRootFolderCapacity(t.Context(), root.ID, settings.RootFolderCapacity{
		Writable: true, TotalBytes: 1_000_000_000_000, FreeBytes: settings.DefaultMinFreeBytes + 700_000_000, CheckedAt: 1,
	}); err != nil {
		t.Fatalf("SetRootFolderCapacity: %v", err)
	}
	searcher := &fakeIndexerSearcher{responses: func(request indexer.SearchRequest) ([]indexer.RawItem, error) {
		if strings.Contains(request.Query, "Homework") || request.Extra["album"] == "Homework" {
			return []indexer.RawItem{rawItem("guid-homework", "Nosi Zada-Homework-1997-FLAC-GROUP")}, nil
		}
		return []indexer.RawItem{rawItem("guid-discovery", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
	}}
	grab := music.Grab{Search: env.search(), Dispatch: env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "usenet", Client: &fakeSabnzbdSender{clientRef: "nzo-1"}},
	})}
	indexers := []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}

	sent, err := grab.SearchAndSend(t.Context(), first.ID, indexers, music.GrabAutomatic, env.clock.Now())
	if err != nil || sent.Sent == nil {
		t.Fatalf("first grab = %+v, %v; want it sent into 700 MB", sent, err)
	}
	held, err := grab.SearchAndSend(t.Context(), second.ID, indexers, music.GrabAutomatic, env.clock.Now())
	if err != nil || held.Sent != nil || held.NoRoom != 1 {
		t.Fatalf("second grab = sent %v, no room %d, %v; want it passed over: 500 MB is already on its way",
			held.Sent, held.NoRoom, err)
	}
}

// A work its library's maximum rating does not admit is never searched for an automatic grab, but a person may still
// grab it (plan 10-libraries L2).
func TestGrab_HoldsAWorkAboveItsLibrarysRating(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{rawItem("guid-flac", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
	}}
	held := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: &fakeSabnzbdSender{clientRef: "nzo-1"}}})
	var asked []int64
	held.Admits = func(_, age int64) bool {
		asked = append(asked, age)
		return false
	}
	grab := music.Grab{Search: env.search(), Dispatch: held}
	indexers := []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}

	result, err := grab.SearchAndSend(t.Context(), item.ID, indexers, music.GrabAutomatic, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchAndSend: %v", err)
	}
	if !reflect.DeepEqual(result, music.GrabResult{Best: dispatch.Best{AboveRating: true}}) || len(searcher.requests) != 0 {
		t.Fatalf("automatic = %+v after %d searches; want held, nothing searched", result, len(searcher.requests))
	}
	if !reflect.DeepEqual(asked, []int64{-1}) {
		t.Errorf("asked about ages %v, want the unrated album's -1", asked)
	}
	own, err := grab.SearchAndSend(t.Context(), item.ID, indexers, dispatch.GrabUser, env.clock.Now())
	if err != nil || own.Sent == nil {
		t.Fatalf("a person's own grab = %+v, %v; want it sent", own, err)
	}
}
