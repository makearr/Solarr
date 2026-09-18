package httpx_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Invariant 61: a credential must not reach an error message or a log line.
// url.URL.Redacted masks userinfo only, so every provider that passes its key
// in the query -- Newznab, SABnzbd, AcoustID -- leaked it on any failure.
func TestRedactURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"newznab apikey", "https://indexer.test/api?apikey=s3cret&t=caps",
			"https://indexer.test/api?apikey=%3Credacted%3E&t=caps"},
		{"acoustid client key", "https://api.acoustid.org/v2/lookup?client=k3y&duration=215",
			"https://api.acoustid.org/v2/lookup?client=%3Credacted%3E&duration=215"},
		{"case does not matter", "https://indexer.test/api?ApiKey=s3cret",
			"https://indexer.test/api?ApiKey=%3Credacted%3E"},
		{"nothing secret is left exactly as it was", "https://musicbrainz.test/ws/2/release?query=b+a&fmt=json",
			"https://musicbrainz.test/ws/2/release?query=b+a&fmt=json"},
		{"userinfo still masked", "https://user:pw@indexer.test/api?t=caps",
			"https://user:xxxxx@indexer.test/api?t=caps"},
		{"both at once", "https://user:pw@indexer.test/api?apikey=s3cret",
			"https://user:xxxxx@indexer.test/api?apikey=%3Credacted%3E"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := url.Parse(test.in)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := httpx.RedactURL(parsed); got != test.want {
				t.Errorf("RedactURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRedactURL_NilIsEmpty(t *testing.T) {
	if got := httpx.RedactURL(nil); got != "" {
		t.Errorf("RedactURL(nil) = %q, want empty", got)
	}
}

// The path that actually matters: a real failing request, reported the way a
// caller sees it. AcoustID answering 400 for a wrong key printed the key.
func TestDo_KeepsTheKeyOutOfBothErrorPaths(t *testing.T) {
	target := "https://provider.test/v2/lookup?client=s3cret&duration=215"

	t.Run("status error", func(t *testing.T) {
		transport := &roundTripper{responses: []*http.Response{respond(http.StatusBadRequest, nil)}}
		client, _, _, _ := newTestClient(t, transport)

		_, err := client.Get(t.Context(), target)
		var status *httpx.StatusError
		if !errors.As(err, &status) {
			t.Fatalf("err = %v, want a StatusError", err)
		}
		if strings.Contains(err.Error(), "s3cret") || strings.Contains(status.URL, "s3cret") {
			t.Errorf("the key reached the error: %v", err)
		}
	})

	t.Run("transport error", func(t *testing.T) {
		transport := &roundTripper{
			responses: []*http.Response{respond(http.StatusOK, nil)},
			errs:      []error{errors.New("dial refused")},
		}
		client, _, _, _ := newTestClient(t, transport)

		_, err := client.Get(t.Context(), target)
		if err == nil {
			t.Fatal("Get err = nil, want the transport error")
		}
		if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("the key reached the error: %v", err)
		}
	})
}
