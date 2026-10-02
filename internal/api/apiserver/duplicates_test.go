package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakeDuplicates struct{}

func (fakeDuplicates) Duplicates(_ context.Context, _ int64, roots []int64, _ int) ([]apiserver.DuplicateGroupRow, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	return []apiserver.DuplicateGroupRow{{
		Artist: "Artist", Title: "Song", Keep: 0, KeepReason: "it meets the library's target quality (FLAC)",
		Copies: []apiserver.DuplicateCopyRow{
			{MediaObjectID: 1, WorkID: 5, Album: "Album", SizeBytes: 900, DurationMs: 215000, AudioCodec: "flac", Lossless: true,
				Paths: []apiserver.DuplicatePathRow{{RootFolderID: roots[0], RelPath: "A/01.flac"}}},
			{MediaObjectID: 2, WorkID: 6, Album: "Comp", SizeBytes: 100, DurationMs: 216000, AudioCodec: "mp3", BitrateKbps: 96,
				Paths: []apiserver.DuplicatePathRow{{RootFolderID: roots[0], RelPath: "B/01.mp3"}}},
		},
	}}, nil
}

func TestCategoryDuplicates_RecommendsOneCopyAndCountsWhatTheOthersFree(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/1/duplicates"))
	var body struct {
		Reclaimable int64 `json:"reclaimable_bytes"`
		Groups      []struct {
			Title      string `json:"title"`
			KeepReason string `json:"keep_reason"`
			Copies     []struct {
				Keep  bool     `json:"keep"`
				Paths []string `json:"paths"`
			} `json:"copies"`
		} `json:"groups"`
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", recorder.Code, recorder.Body)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Reclaimable != 100 || len(body.Groups) != 1 || !body.Groups[0].Copies[0].Keep || body.Groups[0].Copies[1].Keep ||
		body.Groups[0].Copies[1].Paths[0] != "/media/music/B/01.mp3" || body.Groups[0].KeepReason == "" {
		t.Errorf("report = %+v, want the flac kept, the mp3 (100 bytes) reclaimable, full paths", body)
	}
}

func TestCategoryDuplicates_ALibraryTheCallerCannotReadIs404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Private", "/media/private")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/1/duplicates")); recorder.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", recorder.Code)
	}
}
