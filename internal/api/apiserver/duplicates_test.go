package apiserver_test

import (
	"bytes"
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
		Limited     bool  `json:"limited"`
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
	if body.Reclaimable != 100 || body.Limited || len(body.Groups) != 1 || !body.Groups[0].Copies[0].Keep || body.Groups[0].Copies[1].Keep ||
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

type fakeSimilarVideos struct{}

// SimilarVideos answers one group whose distance is the level's, so a test reads which level the request reached.
func (fakeSimilarVideos) SimilarVideos(_ context.Context, roots []int64, level string, _ int) ([]apiserver.SimilarVideoGroupRow, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	distance := map[string]int{"exact": 0, "high": 4, "medium": 8, "low": 10}[level]
	return []apiserver.SimilarVideoGroupRow{{MaxDistance: distance, Videos: []apiserver.SimilarVideoRow{
		{MediaObjectID: 1, SizeBytes: 900, DurationMs: 5_400_000, Width: 1920, Height: 1080, RootFolderID: roots[0], RelPath: "A/film.mkv"},
		{MediaObjectID: 2, SizeBytes: 300, RootFolderID: roots[0], RelPath: "B/film.mp4"},
	}}}, nil
}

func TestCategorySimilarVideos_ListsGroupsWithFullPaths(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Films", "/media/films")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/1/similar-videos"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Groups []struct {
			MaxDistance int `json:"max_distance"`
			Videos      []struct {
				Path string `json:"path"`
			} `json:"videos"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Groups) != 1 || body.Groups[0].MaxDistance != 8 || len(body.Groups[0].Videos) != 2 || body.Groups[0].Videos[1].Path != "/media/films/B/film.mp4" {
		t.Errorf("report = %+v, want one group of two with full paths", body)
	}
}

func TestCategorySimilarVideos_ALibraryTheCallerCannotReadIs404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Private", "/media/private")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/1/similar-videos")); recorder.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", recorder.Code)
	}
}

func TestCategorySimilarVideos_TheLevelReachesTheFinderAndAnUnknownOneIs400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Films", "/media/films")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/1/similar-videos?level=high"))
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"max_distance":4`)) {
		t.Errorf("level=high: status %d, body %s, want the finder asked at 4 bits", recorder.Code, recorder.Body)
	}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/1/similar-videos?level=wild")); recorder.Code != http.StatusBadRequest {
		t.Errorf("level=wild: status %d, want 400", recorder.Code)
	}
}

func TestSimilarVideos_AcrossLibrariesIsForAdministratorsAndNamesEachLibrary(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Films", "/media/films")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	if recorder := fixture.do(authedGet(t, fixture.signIn(t, "listener"), "/api/v1/duplicates/similar-videos")); recorder.Code != http.StatusForbidden {
		t.Fatalf("listener: status %d, want 403", recorder.Code)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	if err := fixture.cache.SetVideoPerceptualHash(t.Context(), fixture.clock, true); err != nil {
		t.Fatal(err)
	}
	recorder := fixture.do(authedGet(t, fixture.signIn(t, "admin"), "/api/v1/duplicates/similar-videos"))
	var body struct {
		HashingEnabled bool `json:"hashing_enabled"`
		Groups         []struct {
			Videos []struct {
				CategoryID int64 `json:"category_id"`
				Width      int64 `json:"width"`
			} `json:"videos"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || recorder.Code != http.StatusOK {
		t.Fatalf("admin: status %d, body %s, err %v", recorder.Code, recorder.Body, err)
	}
	if !body.HashingEnabled || len(body.Groups) != 1 || body.Groups[0].Videos[0].CategoryID != 1 || body.Groups[0].Videos[0].Width != 1920 {
		t.Errorf("report = %+v, want hashing on and the first video in library 1 at width 1920", body)
	}
}
