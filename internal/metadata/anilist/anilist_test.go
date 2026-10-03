package anilist_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type canned struct {
	status  int
	body    string
	request *http.Request
	sent    string
}

func (c *canned) RoundTrip(request *http.Request) (*http.Response, error) {
	c.request = request
	sent, _ := io.ReadAll(request.Body)
	c.sent = string(sent)
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(c.body)), Request: request}, nil
}

func newClient(transport http.RoundTripper) anilist.Client {
	fake := clock.NewFakeAtEpoch()
	return anilist.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100}),
		httpx.ClientConfig{Provider: "anilist-test", UserAgent: "test", Transport: transport}))
}

func TestMediaReadsTitlesSynonymsAndArt(t *testing.T) {
	transport := &canned{body: `{"data":{"Media":{"id":7,"title":{"romaji":"Shingeki Example","english":"Example Titan","native":"native name"},
		"synonyms":["Ex", "Example Titan", "Ataque"],"format":"TV","episodes":25,"status":"FINISHED","seasonYear":2013,
		"bannerImage":"https://example.test/b.jpg","coverImage":{"extraLarge":"https://example.test/c.jpg"},"genres":["Action"],"idMal":99}}}`}
	media, err := newClient(transport).Media(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(media.Titles(), "|"); got != "Shingeki Example|Example Titan|native name|Ex|Ataque" {
		t.Errorf("titles = %q: three titles first, no repeats", got)
	}
	if media.BannerImage == "" || media.CoverImage.ExtraLarge == "" || media.IDMal != 99 || media.Episodes != 25 {
		t.Errorf("media = %+v", media)
	}
	if transport.request.Method != http.MethodPost || !strings.Contains(transport.sent, `"id":7`) || !strings.Contains(transport.sent, "Media(id:$id,type:ANIME)") {
		t.Errorf("request = %s %s", transport.request.Method, transport.sent)
	}
}

func TestAnUnknownIDIsNotFound(t *testing.T) {
	if _, err := newClient(&canned{body: `{"data":{"Media":null},"errors":[{"message":"Not Found."}]}`}).Media(t.Context(), 1); !errors.Is(err, anilist.ErrNotFound) {
		t.Errorf("null media err = %v", err)
	}
	if _, err := newClient(&canned{status: http.StatusNotFound}).Media(t.Context(), 1); !errors.Is(err, anilist.ErrNotFound) {
		t.Errorf("404 err = %v", err)
	}
}
