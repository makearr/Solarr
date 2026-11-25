package listenbrainz_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
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

type pages struct {
	body  string
	asked []string
}

func (p *pages) Do(_ context.Context, request *http.Request) (*http.Response, error) {
	p.asked = append(p.asked, request.URL.String())
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(p.body))}, nil
}

func TestSitewideReleaseGroups_AsksTheRangeAndPageAndReadsTheRows(t *testing.T) {
	answer := &pages{body: `{"payload":{"release_groups":[{"release_group_mbid":"rg-1","release_group_name":"Example Album","artist_name":"Example Artist",` +
		`"artist_mbids":["ar-1"],"caa_release_mbid":"rel-1","listen_count":40}],"total_release_group_count":900}}`}
	rows, total, err := listenbrainz.New(answer).SitewideReleaseGroups(t.Context(), listenbrainz.RangeWeek, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := []listenbrainz.ChartReleaseGroup{{MBID: "rg-1", Title: "Example Album", Artist: "Example Artist", ArtistMBIDs: []string{"ar-1"}, CoverRelease: "rel-1", Listens: 40}}
	if !reflect.DeepEqual(rows, want) || total != 900 {
		t.Errorf("rows %+v total %d", rows, total)
	}
	if asked := "https://api.listenbrainz.org/1/stats/sitewide/release-groups?count=20&offset=20&range=week"; answer.asked[0] != asked {
		t.Errorf("asked %s, want %s", answer.asked[0], asked)
	}
}

func TestUpcomingReleases_AsksOnlyTheComingDays(t *testing.T) {
	answer := &pages{body: `{"payload":{"releases":[{"release_group_mbid":"rg-2","release_name":"Next Album","artist_credit_name":"Example Artist",` +
		`"release_date":"2030-01-02","release_group_primary_type":"Album"}]}}`}
	rows, err := listenbrainz.New(answer).UpcomingReleases(t.Context(), 30)
	if err != nil {
		t.Fatal(err)
	}
	want := []listenbrainz.FreshRelease{{ReleaseGroupMBID: "rg-2", Title: "Next Album", Artist: "Example Artist", ReleasedOn: "2030-01-02", PrimaryType: "Album"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows %+v", rows)
	}
	if asked := "https://api.listenbrainz.org/1/explore/fresh-releases/?days=30&future=true&past=false&sort=release_date"; answer.asked[0] != asked {
		t.Errorf("asked %s, want %s", answer.asked[0], asked)
	}
}
