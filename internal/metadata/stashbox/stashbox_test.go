package stashbox_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct {
	body    string
	headers []http.Header
	sent    []map[string]any
}

func (a *answer) RoundTrip(request *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(request.Body)
	var sent map[string]any
	_ = json.Unmarshal(raw, &sent)
	a.sent = append(a.sent, sent)
	a.headers = append(a.headers, request.Header)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(a.body)), Request: request}, nil
}

func client(t *testing.T, transport http.RoundTripper, key string) stashbox.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return stashbox.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "stashdb", UserAgent: "test", Transport: transport}), "https://stash.example/graphql", func() string { return key })
}

func TestSearchScenesSendsTheKeyAndReadsTheScene(t *testing.T) {
	transport := &answer{body: `{"data":{"searchScene":[{"id":"s1","title":"Night Class","release_date":"2007-03-30","duration":635,
		"studio":{"id":"st1","name":"Studio","parent":{"id":"st0","name":"Network"}},"tags":[{"id":"t1","name":"Teacher"}],
		"performers":[{"as":"Kiky","performer":{"id":"p1","name":"Ada Vale","gender":"FEMALE","aliases":["Kiky"]}}],
		"fingerprints":[{"hash":"72629e7f89597c59","algorithm":"OSHASH","duration":635}]}]}}`}
	scenes, err := client(t, transport, " secret ").SearchScenes(t.Context(), "night class", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []stashbox.Scene{{ID: "s1", Title: "Night Class", ReleaseDate: "2007-03-30", Duration: 635,
		Studio:       &stashbox.Studio{ID: "st1", Name: "Studio", Parent: &stashbox.Studio{ID: "st0", Name: "Network"}},
		Tags:         []stashbox.Tag{{ID: "t1", Name: "Teacher"}},
		Performers:   []stashbox.Appearance{{As: "Kiky", Performer: stashbox.Performer{ID: "p1", Name: "Ada Vale", Gender: "FEMALE", Aliases: []string{"Kiky"}}}},
		Fingerprints: []stashbox.Fingerprint{{Hash: "72629e7f89597c59", Algorithm: stashbox.AlgorithmOSHash, Duration: 635}}}}
	if !reflect.DeepEqual(scenes, want) {
		t.Errorf("scenes = %+v, want %+v", scenes, want)
	}
	if got := transport.headers[0].Get("ApiKey"); got != "secret" {
		t.Errorf("ApiKey header = %q, want the trimmed key", got)
	}
	variables := transport.sent[0]["variables"].(map[string]any)
	if variables["t"] != "night class" || variables["n"] != float64(3) {
		t.Errorf("variables = %v", variables)
	}
}

func TestWithoutAKeyNothingIsAsked(t *testing.T) {
	transport := &answer{body: `{}`}
	if _, err := client(t, transport, "").SearchPerformers(t.Context(), "x", 1); !errors.Is(err, stashbox.ErrNoKey) {
		t.Errorf("err = %v, want ErrNoKey", err)
	}
	if len(transport.sent) != 0 {
		t.Errorf("asked %d times without a key", len(transport.sent))
	}
}

func TestFindSceneNullIsNotFoundAndGraphQLErrorsSurface(t *testing.T) {
	if _, err := client(t, &answer{body: `{"data":{"findScene":null}}`}, "k").FindScene(t.Context(), "x"); !errors.Is(err, stashbox.ErrNotFound) {
		t.Errorf("null scene err = %v, want ErrNotFound", err)
	}
	_, err := client(t, &answer{body: `{"errors":[{"message":"bad field"}]}`}, "k").FindPerformer(t.Context(), "x")
	if err == nil || !strings.Contains(err.Error(), "bad field") {
		t.Errorf("err = %v, want the server's message", err)
	}
}

func TestScenesByFingerprintAsksOneListPerFile(t *testing.T) {
	transport := &answer{body: `{"data":{"findScenesBySceneFingerprints":[[{"id":"s1","title":"A"}],[]]}}`}
	found, err := client(t, transport, "k").ScenesByFingerprint(t.Context(), [][]stashbox.Fingerprint{
		{{Hash: "aa", Algorithm: stashbox.AlgorithmOSHash}}, {{Hash: "bb", Algorithm: stashbox.AlgorithmOSHash}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || len(found[0]) != 1 || found[0][0].ID != "s1" || len(found[1]) != 0 {
		t.Errorf("found = %+v", found)
	}
}
