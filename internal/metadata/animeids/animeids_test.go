package animeids_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/animeids"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type file struct {
	body  string
	reads int
	fail  bool
}

func (f *file) GetJSON(_ context.Context, _ string, _ int64, into any) error {
	f.reads++
	if f.fail {
		return errors.New("down")
	}
	return json.Unmarshal([]byte(f.body), into)
}

const sample = `[
 {"type":"TV","anidb_id":10,"anilist_id":20,"mal_id":30,"tvdb_id":500},
 {"type":"TV","anidb_id":11,"mal_id":31,"tvdb_id":500},
 {"type":"OVA","anidb_id":12,"tvdb_id":500},
 {"type":"MOVIE","anidb_id":13,"tvdb_id":500},
 {"type":"TV","anidb_id":14}
]`

func TestByTVDB_GathersTheSeriesProperAndReadsTheFileOncePerDay(t *testing.T) {
	source, fake := &file{body: sample}, clock.NewFakeAtEpoch()
	idMap := animeids.New(source, fake)
	got, found, err := idMap.ByTVDB(t.Context(), 500)
	want := animeids.Links{AniDB: []int64{10, 11}, MAL: []int64{30, 31}, AniList: []int64{20}}
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %+v, %v, %v; want %+v", got, found, err, want)
	}
	if _, found, _ := idMap.ByTVDB(t.Context(), 999); found {
		t.Error("an unlisted series was found")
	}
	if source.reads != 1 {
		t.Errorf("reads = %d, want 1", source.reads)
	}
	fake.Advance(25 * time.Hour)
	source.fail = true
	got, found, err = idMap.ByTVDB(t.Context(), 500)
	if err == nil || !found || !reflect.DeepEqual(got, want) {
		t.Errorf("a failed refresh must keep the older copy and say so: %+v %v %v", got, found, err)
	}
	_, _, _ = idMap.ByTVDB(t.Context(), 500)
	if source.reads != 2 {
		t.Errorf("reads = %d, want 2: a failed refresh is not retried within the hour", source.reads)
	}
}
