package whisparr_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/whisparr"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct {
	bodies map[string]string
	asked  []string
}

func (a *answer) RoundTrip(request *http.Request) (*http.Response, error) {
	a.asked = append(a.asked, request.URL.String())
	status, body := http.StatusNotFound, `404 page not found`
	if found, ok := a.bodies[request.URL.Path]; ok {
		status, body = http.StatusOK, found
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func client(t *testing.T, transport http.RoundTripper) whisparr.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return whisparr.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "whisparr", UserAgent: "test", Transport: transport}))
}

func TestSearchScenesReadsCreditsAndTheSite(t *testing.T) {
	transport := &answer{bodies: map[string]string{"/v3/scene/search": `[{"Title":"Night Class","Slug":"studio-night-class","Overview":"A lesson.",
		"ReleaseDate":"2007-03-30","Duration":10,"SiteId":77,"Type":"Scene","Genres":["Teacher"],
		"Credits":[{"Name":"Ada Vale","Gender":"Female","ForeignGuid":"p-1","Images":[{"CoverType":"Headshot","Url":"https://cdn.example/a.webp"}]}],
		"ForeignGuid":"s-1","ForeignId":5}]`}}
	scenes, err := client(t, transport).SearchScenes(t.Context(), "night class")
	if err != nil {
		t.Fatal(err)
	}
	want := []whisparr.Scene{{Title: "Night Class", Slug: "studio-night-class", Overview: "A lesson.", ReleaseDate: "2007-03-30", Duration: 10, SiteID: 77,
		Type: "Scene", Genres: []string{"Teacher"}, ForeignGUID: "s-1", ForeignID: 5,
		Credits: []whisparr.Credit{{Name: "Ada Vale", Gender: "Female", ForeignGUID: "p-1", Images: []whisparr.Image{{CoverType: "Headshot", URL: "https://cdn.example/a.webp"}}}}}}
	if !reflect.DeepEqual(scenes, want) {
		t.Errorf("scenes = %+v, want %+v", scenes, want)
	}
	if asked := transport.asked[0]; asked != "https://api.whisparr.com/v3/scene/search?q=night+class" {
		t.Errorf("asked %s", asked)
	}
}

func TestFindSiteTreatsTheEmptySiteAsNotFound(t *testing.T) {
	transport := &answer{bodies: map[string]string{"/v3/site/1": `{"Title":null,"Episodes":[]}`, "/v3/site/2": `{"Title":"Studio","Network":"Group","Episodes":[{"Title":"One"}]}`}}
	whisp := client(t, transport)
	if _, err := whisp.FindSite(t.Context(), 1); !errors.Is(err, whisparr.ErrNotFound) {
		t.Errorf("empty site err = %v, want ErrNotFound", err)
	}
	site, err := whisp.FindSite(t.Context(), 2)
	if err != nil || site.Network != "Group" || len(site.Scenes) != 1 || site.Scenes[0].Title != "One" {
		t.Errorf("site = %+v, err = %v", site, err)
	}
}
