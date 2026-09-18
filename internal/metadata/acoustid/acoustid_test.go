package acoustid_test

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type fakeTransport struct {
	body       string
	lastQuery  url.Values
	calls      int
	statusCode int
}

func (f *fakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f.calls++
	f.lastQuery = request.URL.Query()
	status := f.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(strings.NewReader(f.body)),
		Header: http.Header{"Content-Type": []string{"application/json"}}, Request: request,
	}, nil
}

func newClient(t *testing.T, transport http.RoundTripper, key string) acoustid.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	http := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: acoustid.Name, UserAgent: "test", Transport: transport})
	return acoustid.New(http, "https://acoustid.invalid/v2", func() string { return key })
}

const goodBody = `{"status":"ok","results":[
  {"score":0.95,"recordings":[{"id":"rec-1","releasegroups":[{"id":"rg-1"},{"id":"rg-1"}]}]},
  {"score":0.40,"recordings":[{"id":"rec-weak","releasegroups":[{"id":"rg-weak"}]}]}
]}`

// With no key the rung is disabled, not failing: D12 builds it now and the key
// is entered later, so a caller must be able to tell the two apart.
func TestLookup_WithoutAKeyIsDisabledRatherThanAFailure(t *testing.T) {
	transport := &fakeTransport{body: goodBody}
	client := newClient(t, transport, "")

	if client.Enabled() {
		t.Error("Enabled() = true with no key")
	}
	_, err := client.Lookup(t.Context(), "AQAD", 3*time.Minute)
	if !errors.Is(err, acoustid.ErrNoKey) {
		t.Errorf("Lookup err = %v, want ErrNoKey", err)
	}
	if transport.calls != 0 {
		t.Error("a lookup with no key must not reach the network")
	}
}

// A weak match is a guess, and a wrong identity is worse than none (ADR-24).
func TestLookup_DropsMatchesBelowTheScoreFloor(t *testing.T) {
	client := newClient(t, &fakeTransport{body: goodBody}, "key")

	matches, err := client.Lookup(t.Context(), "AQAD", 3*time.Minute)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %+v, want only the one above the floor", matches)
	}
	if matches[0].Score != 0.95 {
		t.Errorf("Score = %v, want the strong match", matches[0].Score)
	}
}

// A release group named twice by different recordings is one album.
func TestLookup_DeduplicatesReleaseGroups(t *testing.T) {
	client := newClient(t, &fakeTransport{body: goodBody}, "key")

	matches, err := client.Lookup(t.Context(), "AQAD", 3*time.Minute)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches[0].ReleaseGroupMBIDs) != 1 || matches[0].ReleaseGroupMBIDs[0] != "rg-1" {
		t.Errorf("ReleaseGroupMBIDs = %v, want rg-1 once", matches[0].ReleaseGroupMBIDs)
	}
}

// The duration is sent in whole seconds, which is what AcoustID matches on.
func TestLookup_SendsTheFingerprintAndDuration(t *testing.T) {
	transport := &fakeTransport{body: goodBody}
	client := newClient(t, transport, "secret-key")

	if _, err := client.Lookup(t.Context(), "AQADtE", 215*time.Second); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got := transport.lastQuery.Get("fingerprint"); got != "AQADtE" {
		t.Errorf("fingerprint = %q", got)
	}
	if got := transport.lastQuery.Get("duration"); got != "215" {
		t.Errorf("duration = %q, want whole seconds", got)
	}
	if got := transport.lastQuery.Get("client"); got != "secret-key" {
		t.Errorf("client = %q, want the configured key", got)
	}
}

// Most files are simply not in AcoustID; that is an empty answer, not an error.
func TestLookup_NoMatchIsNotAnError(t *testing.T) {
	client := newClient(t, &fakeTransport{body: `{"status":"ok","results":[]}`}, "key")

	matches, err := client.Lookup(t.Context(), "AQAD", time.Minute)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("matches = %+v, want none", matches)
	}
}

// A scored match naming no ids identifies nothing and must not be reported.
func TestLookup_DropsAScoredMatchWithNoIDs(t *testing.T) {
	client := newClient(t, &fakeTransport{body: `{"status":"ok","results":[{"score":0.99,"recordings":[]}]}`}, "key")

	matches, err := client.Lookup(t.Context(), "AQAD", time.Minute)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("matches = %+v, want none -- it identifies nothing", matches)
	}
}

func TestLookup_ReportsARefusal(t *testing.T) {
	client := newClient(t, &fakeTransport{body: `{"status":"error","error":{"message":"invalid API key"}}`}, "key")

	if _, err := client.Lookup(t.Context(), "AQAD", time.Minute); err == nil ||
		!strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("err = %v, want the refusal reported", err)
	}
}

// A literal "+" is not the meta separator: AcoustID then answers bare ids with
// no metadata and no error, which the score filter silently discards. Measured
// against the real service, 2026-09-18.
func TestLookup_AsksForMetadataWithASpaceSeparatedList(t *testing.T) {
	transport := &fakeTransport{body: goodBody}
	client := newClient(t, transport, "key")

	if _, err := client.Lookup(t.Context(), "AQAD", 3*time.Minute); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got := transport.lastQuery.Get("meta"); got != "recordings releasegroups" {
		t.Errorf("meta = %q, want a space-separated list", got)
	}
}

// The names are the point of the rung: a file whose tags say nothing still has
// an artist and a title once its audio is recognised, and those are what the
// ordered metadata sources get searched with (D12).
func TestLookup_CarriesTheNamesAndJoinsAnArtistCredit(t *testing.T) {
	body := `{"status":"ok","results":[{"score":0.98,"recordings":[
	  {"id":"rec-1","title":"Covabapoz Fofu","duration":220.0,
	   "artists":[{"name":"Heka","joinphrase":" & "},{"name":"Roma Ryan"}],
	   "releasegroups":[{"id":"rg-1"}]}]}]}`
	client := newClient(t, &fakeTransport{body: body}, "key")

	matches, err := client.Lookup(t.Context(), "AQAD", 3*time.Minute)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	want := acoustid.Recording{
		MBID: "rec-1", Title: "Covabapoz Fofu", Artist: "Heka & Roma Ryan", Duration: 220 * time.Second,
		ReleaseGroupMBIDs: []string{"rg-1"},
	}
	if len(matches) != 1 || len(matches[0].Recordings) != 1 ||
		!reflect.DeepEqual(matches[0].Recordings[0], want) {
		t.Errorf("recordings = %+v, want %+v", matches, want)
	}
}
