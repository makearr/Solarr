package music_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

// fakeSabnzbdSender answers a canned client ref (or error) to every Add, recording what it was
// asked to send; the rest of download.Client is not needed here.
type fakeSabnzbdSender struct {
	download.Client
	clientRef string
	err       error
	refuse    map[string]bool
	mu        sync.Mutex
	requests  []download.AddRequest
	removed   []string
	// afterAdd runs once the client has accepted a release: another sender winning the race, in a test.
	afterAdd func()
}

func (f *fakeSabnzbdSender) Remove(_ context.Context, clientRef string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, clientRef)
	return nil
}

func (f *fakeSabnzbdSender) Add(_ context.Context, request download.AddRequest) (string, error) {
	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.mu.Unlock()
	if f.afterAdd != nil {
		f.afterAdd()
	}
	if f.refuse[request.Name] {
		return "", fmt.Errorf("fake: %w", download.ErrRefused)
	}
	return f.clientRef, f.err
}

func (env *searchTestEnv) dispatch(downloaders []music.DownloaderConn) music.Dispatch {
	return music.Dispatch{
		Candidates: env.candidates, Acquisitions: env.acquisitions, Desired: env.desired,
		Downloaders: func(context.Context) ([]music.DownloaderConn, error) {
			return downloaders, nil
		},
		Fetch: func(_ context.Context, indexerID int64, link string) ([]byte, error) {
			return []byte(fmt.Sprintf("nzb from indexer %d at %s", indexerID, link)), nil
		},
	}
}

// recordDecision persists a candidate and a decision against itemID directly
// -- Dispatch tests care about what happens after a decision exists, not
// about how SearchItem produced it.
func (env *searchTestEnv) recordDecision(t *testing.T, itemID int64, outcome string) library.DecisionDetail {
	t.Helper()
	candidate, err := env.candidates.Upsert(t.Context(), library.NewReleaseCandidate{
		IndexerID: 1, GUID: "guid-dispatch", Title: "Nosi Zada-Discovery-2001-FLAC-GROUP",
		Protocol: "usenet", DownloadURL: "https://indexer.example/nzb/1", Source: "search",
		FirstSeenAt: env.clock.Now().Unix(), ExpiresAt: env.clock.Now().Unix() + 1_000_000,
	})
	if err != nil {
		t.Fatalf("Upsert candidate: %v", err)
	}
	decision, err := env.candidates.RecordDecision(t.Context(), library.NewDecision{
		CandidateID: candidate.ID, DesiredItemID: itemID, Outcome: outcome, Score: 10,
		ComponentsJSON: "[]", RejectionsJSON: "[]", DeferralsJSON: "[]",
		DecidedAt: env.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	return library.DecisionDetail{Decision: decision, ReleaseCandidate: candidate}
}

func TestDispatch_SendCreatesAQueuedAcquisition(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "ACCEPT")
	sender := &fakeSabnzbdSender{clientRef: "nzo-123"}

	acquisition, err := env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "usenet", Client: sender, Category: "music"},
	}).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if acquisition.State != "QUEUED" || acquisition.DownloadClientRef != "nzo-123" {
		t.Errorf("acquisition = %+v, want state=QUEUED download_client_ref=nzo-123", acquisition)
	}
	if acquisition.DownloaderID != 5 {
		t.Errorf("DownloaderID = %d, want 5", acquisition.DownloaderID)
	}
	// The NZB is fetched from the listing indexer and sent under the release's own name.
	want := []download.AddRequest{{
		Name: "Nosi Zada-Discovery-2001-FLAC-GROUP", File: []byte("nzb from indexer 1 at https://indexer.example/nzb/1"),
		Category: "music",
	}}
	if !reflect.DeepEqual(sender.requests, want) {
		t.Errorf("sender.requests = %+v, want %+v", sender.requests, want)
	}
}

// A grab is history: M1's acceptance test wants a "grabbed" entry for the album.
func TestDispatch_RecordsTheGrab(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "ACCEPT")
	dispatch := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: &fakeSabnzbdSender{clientRef: "nzo-1"}}})
	var recorded []string
	dispatch.History = func(_ context.Context, kind download.HistoryKind, acquisition library.Acquisition, _ string) {
		recorded = append(recorded, fmt.Sprintf("%s %s", kind, acquisition.DownloadClientRef))
	}

	if _, err := dispatch.Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if want := []string{"download.grabbed nzo-1"}; !reflect.DeepEqual(recorded, want) {
		t.Errorf("history = %q, want %q", recorded, want)
	}
}

// Two senders for one item (a hunt and a click) must not both put the release in the client.
func TestDispatch_TwoSendersOfOneItemSendOnce(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "ACCEPT")
	sender := &fakeSabnzbdSender{clientRef: "nzo-1"}
	dispatch := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: sender}})
	dispatch.Locks = &keyedlock.Locks{}

	var wait sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, errs[i] = dispatch.Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
		}()
	}
	wait.Wait()
	sent := 0
	for _, err := range errs {
		if err == nil {
			sent++
		} else if !errors.Is(err, library.ErrAcquisitionAlreadyInFlight) {
			t.Errorf("err = %v, want nil or already in flight", err)
		}
	}
	if sent != 1 || len(sender.requests) != 1 {
		t.Errorf("%d sends succeeded and the client was asked %d times, want exactly one", sent, len(sender.requests))
	}
}

// A release the client accepted but no row records would never be followed: it is taken back out.
func TestDispatch_ARowThatCannotBeRecordedTakesTheReleaseBackOutOfTheClient(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "ACCEPT")
	sender := &fakeSabnzbdSender{clientRef: "nzo-late"}
	// Another sender records its own download between this one's check and its row.
	sender.afterAdd = func() {
		itemID := item.ID
		if _, err := env.acquisitions.Create(t.Context(), library.NewAcquisition{
			DesiredItemID: &itemID, DownloaderID: 5, DownloadClientRef: "nzo-other", Protocol: "usenet", State: "QUEUED",
			CreatedAt: env.clock.Now().Unix(), UpdatedAt: env.clock.Now().Unix(),
		}); err != nil {
			t.Error(err)
		}
	}

	_, err := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: sender}}).
		Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if err == nil {
		t.Fatal("Send succeeded, want the failed row to be reported")
	}
	if want := []string{"nzo-late"}; !reflect.DeepEqual(sender.removed, want) {
		t.Errorf("removed %v, want %v", sender.removed, want)
	}
}

func TestDispatch_RefusesANonAcceptDecision(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "REJECT")
	sender := &fakeSabnzbdSender{clientRef: "nzo-123"}

	_, err := env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "usenet", Client: sender},
	}).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if !errors.Is(err, music.ErrDecisionNotAccepted) {
		t.Errorf("err = %v, want ErrDecisionNotAccepted", err)
	}
	if len(sender.requests) != 0 {
		t.Error("Add was called for a REJECTed decision -- it must never be")
	}
}

func TestDispatch_RefusesWhenNoDownloaderMatchesTheProtocol(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "ACCEPT")

	_, err := env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "torrent", Client: &fakeSabnzbdSender{}},
	}).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if !errors.Is(err, music.ErrNoDownloaderForProtocol) {
		t.Errorf("err = %v, want ErrNoDownloaderForProtocol", err)
	}
}

func TestDispatch_RefusesAnUnknownDecisionID(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	env.recordDecision(t, item.ID, "ACCEPT")

	_, err := env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "usenet", Client: &fakeSabnzbdSender{}},
	}).Send(t.Context(), item.ID, 999_999, env.clock.Now())
	if !errors.Is(err, music.ErrDecisionNotFound) {
		t.Errorf("err = %v, want ErrDecisionNotFound", err)
	}
}

func TestDispatch_RefusesASecondSendWhileTheFirstIsInFlight(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	first := env.recordDecision(t, item.ID, "ACCEPT")
	second := env.recordDecision(t, item.ID, "ACCEPT")

	dispatch := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: &fakeSabnzbdSender{clientRef: "nzo-1"}}})
	if _, err := dispatch.Send(t.Context(), item.ID, first.Decision.ID, env.clock.Now()); err != nil {
		t.Fatalf("first Send: %v", err)
	}

	_, err := dispatch.Send(t.Context(), item.ID, second.Decision.ID, env.clock.Now())
	if !errors.Is(err, library.ErrAcquisitionAlreadyInFlight) {
		t.Errorf("err = %v, want ErrAcquisitionAlreadyInFlight", err)
	}
}

func TestDispatch_DoesNotRecordAnAcquisitionWhenAddFails(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "ACCEPT")
	sender := &fakeSabnzbdSender{err: errors.New("connection refused")}

	_, err := env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "usenet", Client: sender},
	}).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if err == nil {
		t.Fatal("Send succeeded despite Add failing")
	}

	active, err := env.acquisitions.ListActive(t.Context())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("active acquisitions = %+v, want none recorded when the client refused the job", active)
	}
}

// An NZB that cannot be fetched is never sent, and nothing is recorded as acquired.
func TestDispatch_SendsNothingWhenTheReleaseCannotBeFetched(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "ACCEPT")
	sender := &fakeSabnzbdSender{clientRef: "nzo-123"}
	dispatch := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: sender}})
	dispatch.Fetch = func(context.Context, int64, string) ([]byte, error) { return nil, errors.New("410 gone") }

	if _, err := dispatch.Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now()); err == nil {
		t.Fatal("Send succeeded without the release file")
	}
	active, err := env.acquisitions.ListActive(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(sender.requests) != 0 || len(active) != 0 {
		t.Errorf("sent %d, recorded %d; want nothing sent and nothing recorded", len(sender.requests), len(active))
	}
}

// A second send while the first download is on its way never reaches the client.
func TestDispatch_ASecondSendNeverReachesTheClient(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "ACCEPT")
	sender := &fakeSabnzbdSender{clientRef: "nzo-1"}
	dispatch := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: sender}})

	if _, err := dispatch.Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now()); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if _, err := dispatch.Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now()); !errors.Is(err, library.ErrAcquisitionAlreadyInFlight) {
		t.Errorf("second Send = %v, want ErrAcquisitionAlreadyInFlight", err)
	}
	if len(sender.requests) != 1 {
		t.Errorf("client received %d releases, want 1", len(sender.requests))
	}
}
