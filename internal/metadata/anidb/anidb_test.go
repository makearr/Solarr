package anidb_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/anidb"
)

type answer struct {
	body  string
	asked *string
}

func (a answer) Get(_ context.Context, address string) (*http.Response, error) {
	*a.asked = address
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(a.body))}, nil
}

func TestAnimeReadsTheMainTitleAndRating(t *testing.T) {
	var asked string
	body := `<anime id="11" restricted="false"><startdate>2020-04-01</startdate><titles><title xml:lang="x-jat" type="main">Minato no Hikari</title><title xml:lang="en" type="official">Harbour Lights</title></titles>` +
		`<description> A lighthouse. </description><ratings><permanent count="0">0</permanent><temporary count="900">8.31</temporary></ratings></anime>`
	got, err := anidb.New(answer{asked: &asked, body: body}, func() string { return "mylib" }).Anime(t.Context(), 11)
	want := anidb.Anime{ID: 11, Title: "Minato no Hikari", Description: "A lighthouse.", StartDate: "2020-04-01", Score: 8.31, Votes: 900}
	if err != nil || got != want {
		t.Fatalf("Anime = %+v, %v", got, err)
	}
	if !strings.Contains(asked, "client=mylib") || !strings.Contains(asked, "aid=11") {
		t.Errorf("asked %s", asked)
	}
}

func TestAnimeErrors(t *testing.T) {
	var asked string
	for body, want := range map[string]error{
		`<error code="500">banned</error>`:        anidb.ErrBanned,
		`<error code="330">no such anime</error>`: anidb.ErrNotFound,
	} {
		if _, err := anidb.New(answer{asked: &asked, body: body}, func() string { return "c" }).Anime(t.Context(), 1); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", body, err, want)
		}
	}
	asked = ""
	if _, err := anidb.New(answer{asked: &asked, body: ""}, func() string { return "" }).Anime(t.Context(), 1); !errors.Is(err, anidb.ErrNoKey) || asked != "" {
		t.Errorf("err = %v", err)
	}
}
