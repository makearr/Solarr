package listenbrainz_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/listenbrainz"
)

type answer struct {
	body   string
	status int
	asked  *string
}

func (a answer) Do(_ context.Context, request *http.Request) (*http.Response, error) {
	sent, _ := io.ReadAll(request.Body)
	*a.asked = string(sent)
	return &http.Response{StatusCode: a.status, Body: io.NopCloser(strings.NewReader(a.body))}, nil
}

func TestReleaseGroup_ReadsListensAndListenersFromTheAnswer(t *testing.T) {
	var asked string
	client := listenbrainz.New(answer{body: `[{"release_group_mbid":"rg-1","total_listen_count":1200,"total_user_count":90}]`, status: 200, asked: &asked})
	got, err := client.ReleaseGroup(t.Context(), "rg-1")
	if err != nil || got != (listenbrainz.Popularity{Listens: 1200, Listeners: 90}) {
		t.Errorf("ReleaseGroup = %+v, %v", got, err)
	}
	if want := `{"release_group_mbids":["rg-1"]}`; asked != want {
		t.Errorf("sent %s, want %s", asked, want)
	}
}

func TestReleaseGroup_NullCountsMeanNotFound(t *testing.T) {
	var asked string
	client := listenbrainz.New(answer{body: `[{"release_group_mbid":"rg-1","total_listen_count":null,"total_user_count":null}]`, status: 200, asked: &asked})
	if _, err := client.ReleaseGroup(t.Context(), "rg-1"); !errors.Is(err, listenbrainz.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}
