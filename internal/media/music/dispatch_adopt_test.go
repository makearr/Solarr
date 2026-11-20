package music_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/music"
)

// fakeTorrentSender is a torrent client that already holds some torrents and refuses a duplicate.
type fakeTorrentSender struct {
	fakeSabnzbdSender
	held []download.Download
}

func (f *fakeTorrentSender) DownloadsByRef(context.Context, []string) ([]download.Download, error) {
	return f.held, nil
}

func (f *fakeTorrentSender) Add(_ context.Context, request download.AddRequest) (string, error) {
	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.mu.Unlock()
	if len(f.held) > 0 {
		return "", errors.Join(download.ErrRefused, errors.New("fails"))
	}
	return "added", nil
}

func (env *searchTestEnv) torrentDecision(t *testing.T, itemID int64) library.DecisionDetail {
	t.Helper()
	candidate, err := env.candidates.Upsert(t.Context(), library.NewReleaseCandidate{
		IndexerID: 1, GUID: "guid-torrent", Title: "Nosi Zada-Discovery-2001-FLAC-GROUP",
		Protocol: "torrent", DownloadURL: "https://indexer.example/t/1", Source: "search",
		FirstSeenAt: env.clock.Now().Unix(), ExpiresAt: env.clock.Now().Unix() + 1_000_000,
	})
	if err != nil {
		t.Fatalf("Upsert candidate: %v", err)
	}
	decision, err := env.candidates.RecordDecision(t.Context(), library.NewDecision{
		CandidateID: candidate.ID, DesiredItemID: itemID, Outcome: "ACCEPT", Score: 10,
		ComponentsJSON: "[]", RejectionsJSON: "[]", DeferralsJSON: "[]", DecidedAt: env.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	return library.DecisionDetail{Decision: decision, ReleaseCandidate: candidate}
}

func (env *searchTestEnv) torrentDispatch(client download.Client) music.Dispatch {
	d := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "torrent", Client: client, Category: "music"}})
	d.Fetch = func(context.Context, int64, string) ([]byte, error) { return clienttest.TorrentFile("a"), nil }
	return d
}

func TestDispatch_ATorrentTheClientAlreadyHoldsInOurCategoryIsAdopted(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.torrentDecision(t, item.ID)
	hash, err := download.InfoHash(clienttest.TorrentFile("a"))
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeTorrentSender{held: []download.Download{{ClientRef: hash, Category: "music"}}}

	acquisition, err := env.torrentDispatch(client).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if acquisition.DownloadClientRef != hash || len(client.requests) != 0 {
		t.Errorf("ref = %q after %d adds, want the held hash and no add", acquisition.DownloadClientRef, len(client.requests))
	}
}

func TestDispatch_ATorrentHeldUnderAnotherCategoryIsRefusedAndLeftAlone(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.torrentDecision(t, item.ID)
	client := &fakeTorrentSender{held: []download.Download{{ClientRef: "h", Category: "movies"}}}

	_, err := env.torrentDispatch(client).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if !errors.Is(err, download.ErrRefused) || !errors.Is(err, dispatch.ErrInOtherCategory) {
		t.Errorf("err = %v, want a refusal naming the other category", err)
	}
	if len(client.requests) != 0 || len(client.removed) != 0 {
		t.Errorf("requests %d removed %d, want the client untouched", len(client.requests), len(client.removed))
	}
}

func TestDispatch_ATorrentTheClientDoesNotHoldIsAdded(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.torrentDecision(t, item.ID)
	client := &fakeTorrentSender{}

	acquisition, err := env.torrentDispatch(client).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if err != nil || acquisition.DownloadClientRef != "added" || len(client.requests) != 1 {
		t.Errorf("ref %q err %v adds %d, want one add", acquisition.DownloadClientRef, err, len(client.requests))
	}
}
