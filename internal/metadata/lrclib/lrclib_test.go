package lrclib_test

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/lrclib"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type fake struct {
	address string
	body    string
	err     error
}

func (f *fake) GetJSON(_ context.Context, address string, _ int64, into any) error {
	f.address = address
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal([]byte(f.body), into)
}

func TestGet_AsksByNamesAndLength(t *testing.T) {
	source := &fake{body: `{"plainLyrics":"la la","syncedLyrics":"[00:01.00] la la","instrumental":false}`}
	got, found, err := lrclib.New(source).Get(t.Context(), lrclib.Track{Artist: "Ann & Bo", Title: "Song", Album: "Disc", DurationSeconds: 215})
	if err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	want := lrclib.Lyrics{Plain: "la la", Synced: "[00:01.00] la la"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lyrics = %+v, want %+v", got, want)
	}
	parsed, _ := url.Parse(source.address)
	wantQuery := url.Values{"artist_name": {"Ann & Bo"}, "track_name": {"Song"}, "album_name": {"Disc"}, "duration": {"215"}}
	if !reflect.DeepEqual(parsed.Query(), wantQuery) || parsed.Path != "/api/get" {
		t.Errorf("asked %q", source.address)
	}
}

func TestGet_NotFoundAndInstrumentalHaveNoLyrics(t *testing.T) {
	_, found, err := lrclib.New(&fake{err: httpx.ErrNotFound}).Get(t.Context(), lrclib.Track{Artist: "A", Title: "T"})
	if found || err != nil {
		t.Errorf("not found = %v, %v", found, err)
	}
	_, found, err = lrclib.New(&fake{body: `{"instrumental":true,"plainLyrics":"x"}`}).Get(t.Context(), lrclib.Track{Artist: "A", Title: "T"})
	if found || err != nil {
		t.Errorf("instrumental = %v, %v", found, err)
	}
}
