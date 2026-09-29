package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
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
	lastSearchTerms      *apiserver.SearchTerms
	blocklisted          [][2]int64
	lastSearchPartID     int64
	lastListCandidatesID int64
	lastSearchCategoryID int64
	lastSearchWorkIDs    []int64
	grabResult           apiserver.GrabResult
	lastSendWorkID       int64
	lastSendDecisionID   int64
}

func (f *fakeCandidateSearcher) SendCandidate(_ context.Context, workID, decisionID int64) (apiserver.Acquisition, error) {
	f.lastSendWorkID = workID
	f.lastSendDecisionID = decisionID
	return f.acquisition, f.sendErr
}

func (f *fakeCandidateSearcher) SearchWork(
	_ context.Context, workID int64, terms *apiserver.SearchTerms,
) (apiserver.SearchItemResult, error) {
	f.lastSearchWorkID = workID
	f.lastSearchTerms = terms
	return f.searchResult, f.searchErr
}

func (f *fakeCandidateSearcher) BlocklistCandidate(_ context.Context, workID, decisionID int64) error {
	f.blocklisted = append(f.blocklisted, [2]int64{workID, decisionID})
	return nil
}

func (f *fakeCandidateSearcher) SearchTrack(_ context.Context, workID, partID int64) (apiserver.SearchItemResult, error) {
	f.lastSearchWorkID = workID
	f.lastSearchPartID = partID
	return f.searchResult, f.searchErr
}

func (f *fakeCandidateSearcher) ListWorkCandidates(_ context.Context, workID int64) ([]apiserver.CandidateDecision, error) {
	f.lastListCandidatesID = workID
	return f.candidates, f.candidateErr
}

func (f *fakeCandidateSearcher) SearchCategory(_ context.Context, categoryID int64, workIDs []int64) (bool, error) {
	f.lastSearchCategoryID, f.lastSearchWorkIDs = categoryID, workIDs
	return f.duplicate, f.categoryErr
}

func (f *fakeCandidateSearcher) GrabWork(_ context.Context, workID int64) (apiserver.GrabResult, error) {
	f.lastSearchWorkID = workID
	return f.grabResult, f.searchErr
}

// searchableWorkFixture signs in a user holding permission in the one library work 42 is in.
func searchableWorkFixture(t *testing.T, permission settings.Permission) (worksFixture, []*http.Cookie) {
	t.Helper()
	fixture := newWorksFixture(t)
	category, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "member", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, permission); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	fixture.workDetail.row = apiserver.WorkDetailRow{
		ID: 42, MediaType: "music", Title: "KI Pecezinu", SortTitle: "ki pecezinu", RootFolderIDs: []int64{root.ID},
	}
	return fixture, fixture.signIn(t, "member")
}

// Searching, grabbing and sending can start a download: the album's library decides, at request
// (2026-09-25 review). Read shows the candidates; none or another library's album is 404.
func TestWorkAcquisition_NeedsRequestOnTheAlbumsLibrary(t *testing.T) {
	for _, tc := range []struct {
		permission settings.Permission
		method     string
		path       string
		want       int
	}{
		{settings.PermissionRead, http.MethodPost, "/api/v1/works/42/search", http.StatusForbidden},
		{settings.PermissionRead, http.MethodPost, "/api/v1/works/42/grab", http.StatusForbidden},
		{settings.PermissionRead, http.MethodPost, "/api/v1/works/42/candidates/7/send", http.StatusForbidden},
		{settings.PermissionRead, http.MethodGet, "/api/v1/works/42/candidates", http.StatusOK},
		{settings.PermissionRequest, http.MethodPost, "/api/v1/works/42/search", http.StatusOK},
		{settings.PermissionRequest, http.MethodPost, "/api/v1/works/42/grab", http.StatusOK},
		{settings.PermissionRequest, http.MethodPost, "/api/v1/works/42/candidates/7/send", http.StatusOK},
		{settings.PermissionNone, http.MethodPost, "/api/v1/works/42/search", http.StatusNotFound},
		{settings.PermissionNone, http.MethodGet, "/api/v1/works/42/candidates", http.StatusNotFound},
	} {
		fixture, cookies := searchableWorkFixture(t, tc.permission)
		request := authedPost(t, cookies, tc.path, "")
		request.Method = tc.method
		recorder := fixture.do(request)
		searched := fixture.candidateSearcher.lastSearchWorkID + fixture.candidateSearcher.lastSendWorkID
		if recorder.Code != tc.want || (tc.want != http.StatusOK && searched != 0) {
			t.Errorf("%s %s %s: status %d, reached work %d; want %d", tc.permission, tc.method, tc.path,
				recorder.Code, searched, tc.want)
		}
	}
}

func TestSearchWork_ReturnsWhatThePipelineFound(t *testing.T) {
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)

	tier := 0
	fixture.candidateSearcher.searchResult = apiserver.SearchItemResult{
		TierUsed: &tier,
		Decisions: []apiserver.CandidateDecision{{
			ID: 1, CandidateID: 1, Outcome: "ACCEPT", Score: 12.5,
			IndexerID: 1, GUID: "guid-1", Title: "Nosi Zada-Discovery-2001-FLAC-GROUP",
			Protocol: "usenet", Source: "search",
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
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)
	fixture.candidateSearcher.searchErr = apiserver.ErrWorkNotWanted

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/search", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", recorder.Code)
	}
}

func TestSearchWork_NoIndexersConfiguredAnswers409(t *testing.T) {
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)
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
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)
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
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)

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

	music, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatal(err)
	}
	target := "/api/v1/categories/" + strconv.FormatInt(music.ID, 10) + "/search"

	recorder := fixture.do(authedPost(t, cookies, target, ""))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.candidateSearcher.lastSearchCategoryID != music.ID {
		t.Errorf("lastSearchCategoryID = %d, want %d", fixture.candidateSearcher.lastSearchCategoryID, music.ID)
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
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)
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
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)
	fixture.candidateSearcher.sendErr = apiserver.ErrDecisionNotAccepted

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/candidates/7/send", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", recorder.Code)
	}
}

func TestSendCandidate_NoDownloaderAnswers409(t *testing.T) {
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)
	fixture.candidateSearcher.sendErr = apiserver.ErrNoDownloaderForProtocol

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/candidates/7/send", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", recorder.Code)
	}
}

func TestSendCandidate_AlreadyInFlightAnswers409(t *testing.T) {
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)
	fixture.candidateSearcher.sendErr = apiserver.ErrAcquisitionAlreadyInFlight

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/candidates/7/send", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", recorder.Code)
	}
}

func TestSendCandidate_UnknownDecisionAnswers404(t *testing.T) {
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)
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
	music, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatal(err)
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories/"+strconv.FormatInt(music.ID, 10)+"/search", ""))
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

// A track search wants the track before it looks for it, so the button
// cannot report "nothing found" for a track nothing was asking for.
func TestSearchTrack_ReachesTheSearcherWithBothIDs(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/12/parts/34/search", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if got := fixture.candidateSearcher; got.lastSearchWorkID != 12 || got.lastSearchPartID != 34 {
		t.Errorf("searched work %d part %d, want 12 and 34", got.lastSearchWorkID, got.lastSearchPartID)
	}
}

// "Search with different terms": the typed artist and album reach the pipeline; half a pair is refused.
func TestSearchWork_ForwardsDifferentTerms(t *testing.T) {
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/search", `{"artist":" Heka ","album":"Zanuvode"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if got := fixture.candidateSearcher.lastSearchTerms; got == nil || *got != (apiserver.SearchTerms{Artist: "Heka", Album: "Zanuvode"}) {
		t.Errorf("terms = %+v, want Heka / Zanuvode", got)
	}
	recorder = fixture.do(authedPost(t, cookies, "/api/v1/works/42/search", `{"artist":"Heka","album":""}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("half a pair: status %d, want 400", recorder.Code)
	}
}

// Blocklisting a release is an administrator's decision.
func TestBlocklistCandidate_AdministratorOnly(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	fixture.createUser(t, "listener", settings.RoleReadonly)

	recorder := fixture.do(authedPost(t, fixture.signIn(t, "listener"), "/api/v1/works/42/candidates/7/blocklist", ""))
	if recorder.Code != http.StatusForbidden || len(fixture.candidateSearcher.blocklisted) != 0 {
		t.Fatalf("listener: status %d, blocklisted %v; want 403 and nothing", recorder.Code, fixture.candidateSearcher.blocklisted)
	}
	recorder = fixture.do(authedPost(t, fixture.signIn(t, "admin"), "/api/v1/works/42/candidates/7/blocklist", ""))
	if recorder.Code != http.StatusNoContent || len(fixture.candidateSearcher.blocklisted) != 1 ||
		fixture.candidateSearcher.blocklisted[0] != [2]int64{42, 7} {
		t.Errorf("admin: status %d, blocklisted %v; want 204 and [42 7]", recorder.Code, fixture.candidateSearcher.blocklisted)
	}
}

// "Search this page" names its albums; a reader of the library may not start downloads in it.
func TestSearchCategory_SearchesTheNamedAlbumsAndNeedsRequestPermission(t *testing.T) {
	fixture := newWorksFixture(t)
	music, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatal(err)
	}
	target := "/api/v1/categories/" + strconv.FormatInt(music.ID, 10) + "/search"
	fixture.createUser(t, "admin", settings.RoleAdmin)
	admin := fixture.signIn(t, "admin")

	if recorder := fixture.do(authedPost(t, admin, target, `{"work_ids":[3,5]}`)); recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if got := fixture.candidateSearcher.lastSearchWorkIDs; !reflect.DeepEqual(got, []int64{3, 5}) {
		t.Errorf("work ids = %v, want [3 5]", got)
	}

	reader := fixture.createUser(t, "reader", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), reader.ID, music.ID, settings.PermissionRead); err != nil {
		t.Fatal(err)
	}
	fixture.candidateSearcher.lastSearchCategoryID = 0
	recorder := fixture.do(authedPost(t, fixture.signIn(t, "reader"), target, ""))
	if recorder.Code != http.StatusForbidden || fixture.candidateSearcher.lastSearchCategoryID != 0 {
		t.Errorf("reader: status %d, searched %d; want 403 and nothing searched",
			recorder.Code, fixture.candidateSearcher.lastSearchCategoryID)
	}
}

// The automatic search answers what it sent.
func TestGrabWork_AnswersWhatWasSent(t *testing.T) {
	fixture, cookies := searchableWorkFixture(t, settings.PermissionManage)
	fixture.candidateSearcher.grabResult = apiserver.GrabResult{
		Found: 4, Sent: &apiserver.Acquisition{ID: 9, State: "QUEUED", DownloadClientRef: "nzo-9"},
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/42/grab", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Found              int  `json:"found"`
		AlreadyDownloading bool `json:"already_downloading"`
		Sent               *struct {
			ID int64 `json:"id"`
		} `json:"sent"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Found != 4 || body.AlreadyDownloading || body.Sent == nil || body.Sent.ID != 9 ||
		fixture.candidateSearcher.lastSearchWorkID != 42 {
		t.Errorf("body = %+v, searched work %d; want 4 found, acquisition 9 sent, work 42",
			body, fixture.candidateSearcher.lastSearchWorkID)
	}
}
