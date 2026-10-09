package apiserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// fakeWantedParts answers canned held parts.
type fakeWantedParts struct{ held []apiserver.WantedPartRow }

func (f *fakeWantedParts) Missing(context.Context, int64, paginate.TextCursor, int) ([]apiserver.WantedPartRow, error) {
	return nil, nil
}

func (f *fakeWantedParts) Held(context.Context, int64) ([]apiserver.WantedPartRow, error) {
	return f.held, nil
}

// Cutoff unmet lists only the held files below the library's cutoff, judged by what the probe measured, a page at a
// time; a library with no target or cutoff says it cannot judge.
func TestWantedParts_CutoffListsTheFilesBelowTheCutoff(t *testing.T) {
	fixture := newWorksFixture(t)
	library, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	path := fmt.Sprintf("/api/v1/wanted/parts?category_id=%d&view=cutoff&limit=1", library.ID)
	var page struct {
		Items []struct {
			PartID int64 `json:"part_id"`
			File   struct {
				MeetsCutoff *bool `json:"meets_cutoff"`
			} `json:"file"`
		} `json:"items"`
		Judged     bool   `json:"judged"`
		NextCursor string `json:"next_cursor"`
	}
	read := func(target string) {
		t.Helper()
		page.Items, page.NextCursor = nil, ""
		recorder := fixture.do(authedGet(t, cookies, target))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
	}
	read(path)
	if page.Judged || len(page.Items) != 0 {
		t.Fatalf("a library without a cutoff: %+v, want nothing judged", page)
	}

	ids := profileIDs(t, fixture.cache)
	cutoff := ids["Lossless preferred"]
	if err := fixture.cache.SetCategoryQuality(t.Context(), library.ID, []int64{ids["Lossless preferred"], ids["Lossy 320"]}, &cutoff, nil); err != nil {
		t.Fatal(err)
	}
	file := func(codec, ext string, kbps int64, lossless bool) *apiserver.PartFileRow {
		return &apiserver.PartFileRow{MediaObjectID: 1, AudioCodec: codec, Container: "x", Extension: ext, BitrateKbps: kbps, Lossless: lossless}
	}
	fixture.wantedParts.held = []apiserver.WantedPartRow{
		{PartID: 1, File: file("flac", "flac", 900, true)},
		{PartID: 2, File: file("mp3", "mp3", 320, false)},
		{PartID: 3, File: file("flac", "flac", 950, true)},
		{PartID: 4, File: file("aac", "m4a", 128, false)},
	}
	var below []int64
	read(path)
	for {
		for _, item := range page.Items {
			if item.File.MeetsCutoff == nil || *item.File.MeetsCutoff {
				t.Errorf("part %d is listed without a failed cutoff", item.PartID)
			}
			below = append(below, item.PartID)
		}
		if page.NextCursor == "" {
			break
		}
		read(path + "&cursor=" + page.NextCursor)
	}
	if !page.Judged || !reflect.DeepEqual(below, []int64{2, 4}) {
		t.Errorf("below the cutoff, page by page = %v (judged %v), want [2 4]", below, page.Judged)
	}
}
