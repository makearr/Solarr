package anilist_test

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/anilist"
)

func TestSeasonReadsOnePageAndSaysWhetherMoreFollow(t *testing.T) {
	transport := &canned{body: `{"data":{"Page":{"pageInfo":{"hasNextPage":true},"media":[
		{"id":11,"title":{"romaji":"Rei Example","english":"Example Ray"},"format":"TV","seasonYear":2026},
		{"id":12,"title":{"romaji":"Eiga Example"},"format":"MOVIE","seasonYear":2026}]}}}`}
	entries, more, err := newClient(transport).Season(t.Context(), anilist.SeasonFall, 2026, 2)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	if !reflect.DeepEqual(ids, []int64{11, 12}) || !more || entries[1].Format != "MOVIE" {
		t.Errorf("ids = %v, more = %v, entries = %+v", ids, more, entries)
	}
	for _, want := range []string{`"season":"FALL"`, `"year":2026`, `"page":2`, `"perPage":50`, "isAdult:false"} {
		if !strings.Contains(transport.sent, want) {
			t.Errorf("request %s lacks %s", transport.sent, want)
		}
	}
}

func TestSeasonWithoutDataIsAnError(t *testing.T) {
	_, _, err := newClient(&canned{body: `{"data":{"Page":null},"errors":[{"message":"Invalid season"}]}`}).Season(t.Context(), "SOMETIME", 2026, 1)
	if err == nil || !strings.Contains(err.Error(), "Invalid season") {
		t.Errorf("err = %v", err)
	}
}

func TestUserListFlattensEveryListWithItsStatus(t *testing.T) {
	transport := &canned{body: `{"data":{"MediaListCollection":{"lists":[
		{"entries":[{"status":"PLANNING","media":{"id":21,"title":{"romaji":"Planned"},"format":"TV"}}]},
		{"entries":[{"status":"CURRENT","media":{"id":22,"title":{"romaji":"Watching"},"format":"ONA"}},
		            {"status":"CURRENT","media":{"id":23,"title":{"romaji":"Also"},"format":"TV"}}]}]}}}`}
	listed, err := newClient(transport).UserList(t.Context(), "someone")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Status string
		ID     int64
	}
	var got []row
	for _, entry := range listed {
		got = append(got, row{entry.Status, entry.Media.ID})
	}
	want := []row{{"PLANNING", 21}, {"CURRENT", 22}, {"CURRENT", 23}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listed = %+v, want %+v", got, want)
	}
	if !strings.Contains(transport.sent, `"name":"someone"`) {
		t.Errorf("request = %s", transport.sent)
	}
}

func TestAnUnknownUserIsNamedSo(t *testing.T) {
	for name, transport := range map[string]*canned{
		"404":         {status: http.StatusNotFound},
		"null answer": {body: `{"data":{"MediaListCollection":null},"errors":[{"message":"User not found","status":404}]}`},
	} {
		if _, err := newClient(transport).UserList(t.Context(), "nobody"); !errors.Is(err, anilist.ErrUnknownUser) {
			t.Errorf("%s: err = %v, want ErrUnknownUser", name, err)
		}
	}
}
