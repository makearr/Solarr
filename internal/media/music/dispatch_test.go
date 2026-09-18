package music_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/music"
)

// fakeSabnzbdSender answers a canned client ref (or error) to every AddURL
// call, recording what it was asked to submit.
type fakeSabnzbdSender struct {
	clientRef string
	err       error
	requests  []sabnzbd.AddURLRequest
}

func (f *fakeSabnzbdSender) AddURL(_ context.Context, request sabnzbd.AddURLRequest) (string, error) {
	f.requests = append(f.requests, request)
	return f.clientRef, f.err
}

func (env *searchTestEnv) dispatch(downloaders []music.DownloaderConn) music.Dispatch {
	return music.Dispatch{
		Candidates: env.candidates, Acquisitions: env.acquisitions, Desired: env.desired,
		Settings: env.settings,
		Downloaders: func(context.Context) ([]music.DownloaderConn, error) {
			return downloaders, nil
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
		{ID: 5, Protocol: "usenet", Client: sender},
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
	if len(sender.requests) != 1 || sender.requests[0].NZBURL != detail.ReleaseCandidate.DownloadURL {
		t.Errorf("sender.requests = %+v, want one request for %s", sender.requests, detail.ReleaseCandidate.DownloadURL)
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
		t.Error("AddURL was called for a REJECTed decision -- it must never be")
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

func TestDispatch_DoesNotRecordAnAcquisitionWhenAddURLFails(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	detail := env.recordDecision(t, item.ID, "ACCEPT")
	sender := &fakeSabnzbdSender{err: errors.New("connection refused")}

	_, err := env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "usenet", Client: sender},
	}).Send(t.Context(), item.ID, detail.Decision.ID, env.clock.Now())
	if err == nil {
		t.Fatal("Send succeeded despite AddURL failing")
	}

	active, err := env.acquisitions.ListActive(t.Context())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("active acquisitions = %+v, want none recorded when the client refused the job", active)
	}
}
