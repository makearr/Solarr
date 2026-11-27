package adapters

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct {
	status int
	body   string
	err    error
}

type answeringTransport func(*http.Request) answer

func (f answeringTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	given := f(request)
	if given.err != nil {
		return nil, given.err
	}
	return &http.Response{StatusCode: given.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(given.body)), Request: request}, nil
}

func answeringClient(transport http.RoundTripper) *httpx.Client {
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{Provider: "arr-test", UserAgent: "test", Transport: transport})
}

func TestArrTestConnectionNamesTheAppOrWhatToFix(t *testing.T) {
	cases := map[string]struct {
		answer answer
		want   apiserver.ArrConnectionTest
	}{
		"fine":         {answer{status: 200, body: `{"appName":"Radarr","version":"5.2.1"}`}, apiserver.ArrConnectionTest{OK: true, AppName: "Radarr", Version: "5.2.1"}},
		"wrong key":    {answer{status: 401, body: `{}`}, apiserver.ArrConnectionTest{Problem: apiserver.ArrProblemWrongKey}},
		"not an arr":   {answer{status: 200, body: `<html>`}, apiserver.ArrConnectionTest{Problem: apiserver.ArrProblemWrongPort}},
		"no such path": {answer{status: 404, body: `nope`}, apiserver.ArrConnectionTest{Problem: apiserver.ArrProblemWrongPort}},
	}
	for name, test := range cases {
		sync := ArrSync{Lidarr: func(baseURL, apiKey string) arrimport.LidarrClient {
			return arrimport.NewLidarrClient(answeringClient(answeringTransport(func(*http.Request) answer { return test.answer })), baseURL, apiKey)
		}}
		got := sync.TestConnection(t.Context(), settings.ArrAppRadarr, "http://radarr.local:7878", "key")
		got.Message = ""
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: got %+v, want %+v", name, got, test.want)
		}
	}
}

func TestArrDiscoverKeepsOneAnswerPerAppAndSkipsWhatIsNotThere(t *testing.T) {
	transport := answeringTransport(func(request *http.Request) answer {
		switch request.URL.Host {
		case "radarr:7878", "localhost:7878":
			return answer{status: 200, body: `{"status":"OK"}`}
		case "localhost:8080":
			if request.URL.Path == "/api" {
				return answer{status: 200, body: `{"version":"4.3.2"}`}
			}
			return answer{status: 403, body: "Forbidden"}
		}
		return answer{status: 200, body: `<html>some other site</html>`}
	})
	sync := ArrSync{Probe: func(string) *httpx.Client { return answeringClient(transport) }}
	got := sync.Discover(t.Context())
	want := []apiserver.DiscoveredService{
		{Kind: "radarr", Host: "radarr", Port: 7878, BaseURL: "http://radarr:7878"},
		{Kind: "sabnzbd", Host: "localhost", Port: 8080, BaseURL: "http://localhost:8080", Version: "4.3.2"},
		{Kind: "qbittorrent", Host: "localhost", Port: 8080, BaseURL: "http://localhost:8080"},
	}
	if len(got) != len(want) {
		t.Fatalf("found %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("found[%d] = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func TestArrTestConnectionCarriesTheLanguagesOfRadarrsProfiles(t *testing.T) {
	transport := answeringTransport(func(request *http.Request) answer {
		if request.URL.Path == "/api/v3/qualityprofile" {
			return answer{status: 200, body: `[{"id":1,"language":{"name":"Japanese"}},{"id":2,"language":{"name":"English"}},{"id":3,"language":{"name":"Any"}}]`}
		}
		return answer{status: 200, body: `{"appName":"Radarr","version":"5.2.1"}`}
	})
	sync := ArrSync{Lidarr: func(baseURL, apiKey string) arrimport.LidarrClient {
		return arrimport.NewLidarrClient(answeringClient(transport), baseURL, apiKey)
	}}
	got := sync.TestConnection(t.Context(), settings.ArrAppRadarr, "http://radarr.local:7878", "key")
	if want := (apiserver.ArrConnectionTest{OK: true, AppName: "Radarr", Version: "5.2.1", Languages: []string{"ja", "en"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
