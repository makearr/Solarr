package myanimelist_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/myanimelist"
)

type answer struct {
	body  string
	asked *http.Request
}

func (a answer) Do(_ context.Context, request *http.Request) (*http.Response, error) {
	*a.asked = *request
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(a.body))}, nil
}

func TestAnimeReadsScoreAndSendsTheClientID(t *testing.T) {
	var asked http.Request
	client := myanimelist.New(answer{asked: &asked, body: `{"id":5,"title":"Harbour Lights","mean":8.12,"num_scoring_users":4000,"synopsis":"A lighthouse.","start_date":"2020-04-01","main_picture":{"medium":"m","large":"l"}}`}, func() string { return "cid" })
	got, err := client.Anime(t.Context(), 5)
	want := myanimelist.Anime{ID: 5, Title: "Harbour Lights", Synopsis: "A lighthouse.", StartDate: "2020-04-01", Score: 8.12, Votes: 4000, Poster: "l"}
	if err != nil || got != want {
		t.Fatalf("Anime = %+v, %v", got, err)
	}
	if asked.Header.Get("X-MAL-CLIENT-ID") != "cid" || !strings.HasSuffix(asked.URL.Path, "/anime/5") {
		t.Errorf("asked %v %v", asked.URL, asked.Header)
	}
}

func TestAnimeWithoutAClientIDAsksNothing(t *testing.T) {
	var asked http.Request
	client := myanimelist.New(answer{asked: &asked, body: `{}`}, func() string { return "" })
	if _, err := client.Anime(t.Context(), 5); !errors.Is(err, myanimelist.ErrNoKey) || asked.URL != nil {
		t.Errorf("err = %v", err)
	}
}
