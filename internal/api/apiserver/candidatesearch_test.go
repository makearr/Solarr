package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
)

// fakeCandidateSearcher serves apiserver.CandidateSearcher over canned
// results, recording every call it saw.
type fakeCandidateSearcher struct {
	searchResult apiserver.SearchItemResult
	searchErr    error
	candidates   []apiserver.CandidateDecision
	candidateErr error
	duplicate    bool
	categoryErr  error
	acquisition  apiserver.Acquisition
	sendErr      error

	lastSearchWorkID     int64
	lastListCandidatesID int64
	lastSearchCategoryID int64
	lastSendWorkID       int64
	lastSendDecisionID   int64
}

func (f *fakeCandidateSearcher) SendCandidate(_ context.Context, workID, decisionID int64) (apiserver.Acquisition, error) {
	f.lastSendWorkID = workID
	f.lastSendDecisionID = decisionID
	return f.acquisition, f.sendErr
}

func (f *fakeCandidateSearcher) SearchWork(_ context.Context, workID int64) (apiserver.SearchItemResult, error) {
	f.lastSearchWorkID = workID
	return f.searchResult, f.searchErr
}

func (f *fakeCandidateSearcher) ListWorkCandidates(_ context.Context, workID int64) ([]apiserver.CandidateDecision, error) {
	f.lastListCandidatesID = workID
	return f.candidates, f.candidateErr
}

func (f *fakeCandidateSearcher) SearchCategory(_ context.Context, categoryID int64) (bool, error) {
	f.lastSearchCategoryID = categoryID
	return f.duplicate, f.categoryErr
}

func TestSearchWork_ReturnsWhatThePipelineFound(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	tier := 0
	fixture.candidateSearcher.searchResult = apiserver.SearchItemResult{
		TierUsed: &tier,
		Decisions: []apiserver.CandidateDecision{{
			ID: 1, CandidateID: 1, Outcome: "ACCEPT", Score: 12.5,
			IndexerID: 1, GUID: "guid-1", Title: "Nosi Zada-Discovery-2001-FLAC-GROUP",
			Protocol: "usenet", DownloadURL: "https://indexer/nzb/1", Source: "search",
		}},
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/search", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.candidateSearcher.lastSearchWorkID != 42 {
		t.Errorf("lastSearchWorkID = %d, want 42", fixture.candidateSearcher.lastSearchWorkID)
	}
	var body struct {
		TierUsed  *int `json:"tier_used"`
		Decisions []struct {
			Outcome string `json:"outcome"`
			Title   string `json:"title"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.TierUsed == nil || *body.TierUsed != 0 {
		t.Errorf("tier_used = %v, want 0", body.TierUsed)
	}
	if len(body.Decisions) != 1 || body.Decisions[0].Outcome != "ACCEPT" {
		t.Errorf("decisions = %+v, want one ACCEPT", body.Decisions)
	}
}

func TestSearchWork_NotWantedAnswers409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.candidateSearcher.searchErr = apiserver.ErrWorkNotWanted

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/search", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", recorder.Code)
	}
}

func TestSearchWork_NoIndexersConfiguredAnswers409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.candidateSearcher.searchErr = apiserver.ErrNoIndexersConfigured

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/search", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", recorder.Code)
	}
}

func TestSearchWork_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)

	recorder := fixture.do(authedPost(t, nil, "/api/v1/works/42/search", ""))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}
}

func TestListWorkCandidates_ReturnsTheHistory(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.candidateSearcher.candidates = []apiserver.CandidateDecision{
		{ID: 1, Outcome: "REJECT", Title: "old one"},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/42/candidates"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.candidateSearcher.lastListCandidatesID != 42 {
		t.Errorf("lastListCandidatesID = %d, want 42", fixture.candidateSearcher.lastListCandidatesID)
	}
	var decisions []struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decisions); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Outcome != "REJECT" {
		t.Errorf("decisions = %+v, want one REJECT", decisions)
	}
}

func TestListWorkCandidates_EmptyIsNotAnError(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/42/candidates"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if recorder.Body.String() != "[]" && recorder.Body.String() != "[]\n" {
		t.Errorf("body = %s, want an empty array for a work never searched", recorder.Body.String())
	}
}

func TestSearchCategory_EnqueuesAndAnswers202(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories/7/search", ""))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.candidateSearcher.lastSearchCategoryID != 7 {
		t.Errorf("lastSearchCategoryID = %d, want 7", fixture.candidateSearcher.lastSearchCategoryID)
	}
	var body struct {
		Duplicate bool `json:"duplicate"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Duplicate {
		t.Error("duplicate = true, want false for a first run")
	}
}

func TestSendCandidate_ReturnsTheCreatedAcquisition(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.candidateSearcher.acquisition = apiserver.Acquisition{
		ID: 1, DownloaderID: 5, DownloadClientRef: "nzo-123", Protocol: "usenet", State: "QUEUED",
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/candidates/7/send", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.candidateSearcher.lastSendWorkID != 42 || fixture.candidateSearcher.lastSendDecisionID != 7 {
		t.Errorf("lastSendWorkID/DecisionID = %d/%d, want 42/7",
			fixture.candidateSearcher.lastSendWorkID, fixture.candidateSearcher.lastSendDecisionID)
	}
	var body struct {
		DownloadClientRef string `json:"download_client_ref"`
		State             string `json:"state"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.DownloadClientRef != "nzo-123" || body.State != "QUEUED" {
		t.Errorf("body = %+v, want nzo-123/QUEUED", body)
	}
}

func TestSendCandidate_NotAcceptedAnswers409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.candidateSearcher.sendErr = apiserver.ErrDecisionNotAccepted

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/candidates/7/send", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", recorder.Code)
	}
}

func TestSendCandidate_NoDownloaderAnswers409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.candidateSearcher.sendErr = apiserver.ErrNoDownloaderForProtocol

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/candidates/7/send", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", recorder.Code)
	}
}

func TestSendCandidate_AlreadyInFlightAnswers409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.candidateSearcher.sendErr = apiserver.ErrAcquisitionAlreadyInFlight

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/candidates/7/send", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", recorder.Code)
	}
}

func TestSendCandidate_UnknownDecisionAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.candidateSearcher.sendErr = apiserver.ErrCandidateDecisionNotFound

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/candidates/999/send", ""))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

func TestSendCandidate_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)

	recorder := fixture.do(authedPost(t, nil, "/api/v1/works/42/candidates/7/send", ""))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}
}

func TestSearchCategory_ReportsADuplicateRun(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.candidateSearcher.duplicate = true

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories/7/search", ""))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Duplicate bool `json:"duplicate"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Duplicate {
		t.Error("duplicate = false, want true -- a search was already running")
	}
}
