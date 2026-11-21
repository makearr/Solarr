package music_test

import (
	"context"
	"errors"
	"strings"
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
	// lookupErr fails every look-up; heldAfterFirstLookup is what later look-ups find (the torrent arrived meanwhile).
	lookupErr            error
	heldAfterFirstLookup []download.Download
	lookups              int
}

func (f *fakeTorrentSender) DownloadsByRef(context.Context, []string) ([]download.Download, error) {
	f.lookups++
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	if f.lookups > 1 && f.heldAfterFirstLookup != nil {
		return f.heldAfterFirstLookup, nil
	}
	return f.held, nil
}

func (f *fakeTorrentSender) Add(_ context.Context, request download.AddRequest) (string, error) {
	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.mu.Unlock()
	if len(f.held) > 0 || f.heldAfterFirstLookup != nil {
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

// A look-up that fails is no reason to hold the release back: the add goes ahead and the client decides.
func TestDispatch_ATorrentWhoseLookUpFailsIsStillAdded(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.torrentDecision(t, item.ID)
	client := &fakeTorrentSender{lookupErr: errors.New("client unreachable")}

	acquisition, err := env.torrentDispatch(client).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if err != nil || acquisition.DownloadClientRef != "added" || len(client.requests) != 1 {
		t.Errorf("ref %q err %v adds %d, want one add", acquisition.DownloadClientRef, err, len(client.requests))
	}
}

// A duplicate refused because the torrent arrived since the look-up is adopted, not reported as the release's fault.
func TestDispatch_ATorrentRefusedAsADuplicateThatJustArrivedIsAdopted(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.torrentDecision(t, item.ID)
	hash, err := download.InfoHash(clienttest.TorrentFile("a"))
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeTorrentSender{heldAfterFirstLookup: []download.Download{{ClientRef: hash, Category: "music"}}}

	acquisition, err := env.torrentDispatch(client).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if err != nil || acquisition.DownloadClientRef != hash {
		t.Errorf("ref %q err %v, want the torrent adopted by its hash", acquisition.DownloadClientRef, err)
	}
}

// A dead .torrent link falls back to the magnet link the indexer listed with it (IDX-1 step 1b.4).
func TestDispatch_ADeadTorrentLinkFallsBackToTheMagnetLink(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	candidate, err := env.candidates.Upsert(t.Context(), library.NewReleaseCandidate{
		IndexerID: 1, GUID: "guid-magnet", Title: "Nosi Zada-Discovery-2001-FLAC-GROUP", Protocol: "torrent",
		DownloadURL: "https://indexer.example/t/dead", MagnetURL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		Source: "search", FirstSeenAt: env.clock.Now().Unix(), ExpiresAt: env.clock.Now().Unix() + 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := env.candidates.RecordDecision(t.Context(), library.NewDecision{
		CandidateID: candidate.ID, DesiredItemID: item.ID, Outcome: "ACCEPT", Score: 10,
		ComponentsJSON: "[]", RejectionsJSON: "[]", DeferralsJSON: "[]", DecidedAt: env.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeTorrentSender{}
	d := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "torrent", Client: client, Category: "music"}})
	var asked []string
	d.Fetch = func(_ context.Context, _ int64, link string) ([]byte, error) {
		asked = append(asked, link)
		if strings.HasPrefix(link, "magnet:") {
			return []byte(link), nil
		}
		return nil, errors.New("404 gone")
	}
	if _, err := d.Send(t.Context(), item.ID, decision.ID, env.clock.Now()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(asked) != 2 || len(client.requests) != 1 || !strings.HasPrefix(string(client.requests[0].File), "magnet:") {
		t.Errorf("asked %v, client got %d requests; want the dead link then the magnet, sent once", asked, len(client.requests))
	}
}
