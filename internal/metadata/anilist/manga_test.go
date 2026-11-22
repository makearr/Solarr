package anilist_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/anilist"
)

const mangaAnswer = `{"id":31,"idMal":801,"title":{"romaji":"Silent Tide","english":"The Silent Tide","native":"native"},"synonyms":["Tide"],"format":"MANGA","chapters":120,"volumes":12,
"status":"FINISHED","startDate":{"year":2014},"countryOfOrigin":"JP","isAdult":false,"description":"d","genres":["Drama"],"tags":[{"name":"Sea","isAdult":false}],
"coverImage":{"extraLarge":"https://example.test/m.jpg"},"staff":{"edges":[{"role":"Story & Art","node":{"name":{"full":"Mio Sato"}}}]}}`

func TestMangaReadsVolumesAdultFlagAndStaff(t *testing.T) {
	transport := &canned{body: `{"data":{"Media":` + mangaAnswer + `}}`}
	m, err := newClient(transport).Manga(t.Context(), 31)
	if err != nil {
		t.Fatal(err)
	}
	if m.Volumes != 12 || m.Chapters != 120 || m.IDMal != 801 || m.IsAdult || m.StartDate.Year != 2014 || m.CountryOfOrigin != "JP" || m.Format != "MANGA" {
		t.Errorf("manga = %+v", m)
	}
	if got := strings.Join(m.Titles(), "|"); got != "Silent Tide|The Silent Tide|native|Tide" {
		t.Errorf("titles = %q", got)
	}
	if people := m.People(); len(people) != 1 || people[0] != (anilist.MangaPerson{Name: "Mio Sato", Role: "Story & Art"}) {
		t.Errorf("people = %+v", people)
	}
	if !strings.Contains(transport.sent, "type:MANGA") {
		t.Errorf("asked %q; want a MANGA query", transport.sent)
	}
}

func TestSearchMangaPagesAndMissingManga(t *testing.T) {
	transport := &canned{body: `{"data":{"Page":{"pageInfo":{"hasNextPage":true},"media":[` + mangaAnswer + `]}}}`}
	found, more, err := newClient(transport).SearchManga(t.Context(), "silent tide", 1)
	if err != nil || !more || len(found) != 1 || found[0].ID != 31 {
		t.Fatalf("found = %+v more=%v err=%v", found, more, err)
	}
	missing := &canned{body: `{"data":{"Media":null},"errors":[{"message":"Not Found.","status":404}]}`}
	if _, err := newClient(missing).Manga(t.Context(), 1); !errors.Is(err, anilist.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}
