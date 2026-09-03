package apiserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/settings"
)

func authedPost(t *testing.T, cookies []*http.Cookie, target, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	return request
}

const losslessProfileRules = `{"compare":["lossless","score"],"scoring":[
	{"name":"preferred-groups","score":10,"specs":[{"field":"release_group","in":"$preferred"}]}
]}`

func TestSimulateScore_ReturnsAParsedTitleAndADecision(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	profile, err := fixture.cache.CreateQualityProfile(t.Context(), "lossless", losslessProfileRules, `{}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/score/simulate",
		`{"title":"Artist - Album (2024) [FLAC]","quality_profile_id":`+strconv.FormatInt(profile.ID, 10)+`}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}

	var got gen.ScoreSimulation
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Parsed.Artist == nil || *got.Parsed.Artist != "Artist" {
		t.Errorf("Parsed.Artist = %v, want \"Artist\"", got.Parsed.Artist)
	}
	if got.Parsed.Format == nil || *got.Parsed.Format != "flac" {
		t.Errorf("Parsed.Format = %v, want \"flac\"", got.Parsed.Format)
	}
	if len(got.Decision.Quality) != 2 {
		t.Fatalf("Decision.Quality = %+v, want 2 tiers (lossless, score)", got.Decision.Quality)
	}
	if got.Decision.Quality[0].Dimension != "lossless" || got.Decision.Quality[0].Rank != 0 {
		t.Errorf("lossless tier = %+v, want rank 0 (a FLAC title is lossless)", got.Decision.Quality[0])
	}
}

// preferred_release_groups is the caller-supplied allowlist a real evaluation
// would use for the "$preferred" placeholder -- prove it actually reaches the
// interpreter, not just that the request is accepted.
func TestSimulateScore_PreferredReleaseGroupsAffectsScoring(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	profile, err := fixture.cache.CreateQualityProfile(t.Context(), "lossless", losslessProfileRules, `{}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}

	// A real corpus fixture (internal/parse/testdata/releases), not a guess: the
	// golden corpus test already proves this string parses to release_group=FORSAKEN.
	body := `{"title":"Vonan-No_Hopub_II-CD-FLAC-2014-FORSAKEN","quality_profile_id":` + strconv.FormatInt(profile.ID, 10) +
		`,"preferred_release_groups":["FORSAKEN"]}`
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/score/simulate", body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	var got gen.ScoreSimulation
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Parsed.ReleaseGroup == nil || *got.Parsed.ReleaseGroup != "FORSAKEN" {
		t.Fatalf("Parsed.ReleaseGroup = %v, want \"FORSAKEN\" -- test assumption about the parser broke", got.Parsed.ReleaseGroup)
	}
	if len(got.Decision.Components) != 1 || got.Decision.Components[0].Rule != "preferred-groups" {
		t.Errorf("Decision.Components = %+v, want exactly the preferred-groups rule to fire", got.Decision.Components)
	}
	if got.Decision.Score != 10 {
		t.Errorf("Decision.Score = %v, want 10", got.Decision.Score)
	}
}

func TestSimulateScore_UnknownQualityProfileAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/score/simulate",
		`{"title":"Artist - Album","quality_profile_id":999}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown quality_profile_id", recorder.Code)
	}
}

func TestSimulateScore_EmptyTitleAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	profile, err := fixture.cache.CreateQualityProfile(t.Context(), "lossless", losslessProfileRules, `{}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/score/simulate",
		`{"title":"","quality_profile_id":`+strconv.FormatInt(profile.ID, 10)+`}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an empty title", recorder.Code)
	}
}

func TestSimulateScore_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/score/simulate",
		strings.NewReader(`{"title":"x","quality_profile_id":1}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := fixture.do(request)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 with no session", recorder.Code)
	}
}
