package apiserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

type qualityBody struct {
	Profiles []struct {
		Name string `json:"name"`
	} `json:"profiles"`
	Available []struct {
		Name string `json:"name"`
	} `json:"available"`
	CutoffProfileID *int64   `json:"cutoff_profile_id"`
	Warnings        []string `json:"warnings"`
	Target          *struct {
		Name string `json:"name"`
	} `json:"target"`
	AvailableTargets []struct {
		Name string `json:"name"`
	} `json:"available_targets"`
}

func profileIDs(t *testing.T, cache *settings.Cache) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	for _, profile := range cache.Snapshot().QualityProfiles() {
		ids[profile.Name] = profile.ID
	}
	return ids
}

func names(refs []struct {
	Name string `json:"name"`
}) []string {
	out := []string{}
	for _, ref := range refs {
		out = append(out, ref.Name)
	}
	return out
}

// A target is flagged only when no profile of the library takes it: in the second is as good as in the
// first (2026-09-30, replacing 2026-09-19's first-profile-only warning).
func TestLibraryQuality_SaveAndWarnWhenTheTargetFitsNoProfile(t *testing.T) {
	fixture, cookies, id := adminWithLibrary(t)
	ids := profileIDs(t, fixture.cache)
	path := "/api/v1/categories/" + strconv.FormatInt(id, 10) + "/quality"
	aac := `"target":{"audio_codec":"aac","container":"m4a","bitrate_kbps":256,"tolerance_pct":10}`

	targetCount := 0
	// createTarget makes a named music target from the old inline form and answers the member that picks it.
	createTarget := func(inline string) string {
		t.Helper()
		targetCount++
		body := fmt.Sprintf(`{"name":"Target %d","media_type":"music",%s}`, targetCount, strings.TrimSuffix(strings.TrimPrefix(inline, `"target":{`), `}`))
		recorder := fixture.do(authedPost(t, cookies, "/api/v1/qualitytargets", body))
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create target: status = %d, %s", recorder.Code, recorder.Body)
		}
		var created struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf(`"target_id":%d`, created.ID)
	}
	put := func(first, second, target string) qualityBody {
		t.Helper()
		target = createTarget(target)
		profiles := fmt.Sprintf("%d", ids[first])
		if second != "" {
			profiles += fmt.Sprintf(",%d", ids[second])
		}
		body := fmt.Sprintf(`{"profile_ids":[%s],"cutoff_profile_id":%d,%s}`, profiles, ids[first], target)
		recorder := fixture.do(authedPut(t, cookies, path, body))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
		}
		var saved qualityBody
		if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
			t.Fatal(err)
		}
		return saved
	}

	fits := put("Lossy 320", "Lossless preferred", aac)
	if len(fits.Warnings) != 0 {
		t.Errorf("aac 256 under Lossy 320 warned: %v", fits.Warnings)
	}
	if got := names(fits.Profiles); !reflect.DeepEqual(got, []string{"Lossy 320", "Lossless preferred"}) {
		t.Errorf("profiles = %v", got)
	}
	if got := names(fits.Available); got[0] != "Space conscious" || got[1] != "Lossy 320" || got[2] != "Lossless preferred" {
		t.Errorf("available = %v, want the suggested order first", got)
	}

	second := put("Lossless preferred", "Lossy 320", aac)
	if len(second.Warnings) != 0 {
		t.Errorf("aac 256 in the second profile warned: %v", second.Warnings)
	}

	// The reported case: AAC is Space conscious' second codec, and 160 kbps its AAC ideal.
	aac160 := `"target":{"audio_codec":"aac","container":"m4a","bitrate_kbps":160,"tolerance_pct":10}`
	if secondTier := put("Space conscious", "", aac160); len(secondTier.Warnings) != 0 {
		t.Errorf("aac 160 under Space conscious warned: %v", secondTier.Warnings)
	}

	aac128 := `"target":{"audio_codec":"aac","container":"m4a","bitrate_kbps":128,"tolerance_pct":10}`
	misses := put("Lossy 320", "", aac128)
	if len(misses.Warnings) != 1 || !strings.Contains(misses.Warnings[0], "Lossy 320: its bitrate range is 224kbps to 320kbps") {
		t.Errorf("aac 128 under Lossy 320 alone: warnings = %v, want one naming the profile and why", misses.Warnings)
	}

	var reread qualityBody
	if err := json.Unmarshal(fixture.do(authedGet(t, cookies, path)).Body.Bytes(), &reread); err != nil {
		t.Fatal(err)
	}
	if reread.CutoffProfileID == nil || *reread.CutoffProfileID != ids["Lossy 320"] || len(reread.Warnings) != 1 {
		t.Errorf("read back: cutoff = %v warnings = %v", reread.CutoffProfileID, reread.Warnings)
	}
}

func TestLibraryQuality_ProblemsAreA400(t *testing.T) {
	fixture, cookies, id := adminWithLibrary(t)
	path := "/api/v1/categories/" + strconv.FormatInt(id, 10) + "/quality"
	recorder := fixture.do(authedPut(t, cookies, path, `{"profile_ids":[999],"target_id":4242}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
	for _, want := range []string{"profiles[0]", "target"} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("the problem does not name %s: %s", want, recorder.Body)
		}
	}
	bad := fixture.do(authedPost(t, cookies, "/api/v1/qualitytargets", `{"name":"x","media_type":"music","audio_codec":"","container":"m4a","tolerance_pct":90}`))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad target: status = %d, want 400 (%s)", bad.Code, bad.Body)
	}
	for _, want := range []string{"target.audio_codec", "target.tolerance_pct"} {
		if !strings.Contains(bad.Body.String(), want) {
			t.Errorf("the problem does not name %s: %s", want, bad.Body)
		}
	}
}

// A delay beyond the bound is the caller's mistake, named, never a 500 (found 2026-10-02).
func TestLibraryQuality_ATooLongDelayIsA400(t *testing.T) {
	fixture, cookies, id := adminWithLibrary(t)
	path := "/api/v1/categories/" + strconv.FormatInt(id, 10) + "/quality"
	recorder := fixture.do(authedPut(t, cookies, path,
		`{"profile_ids":[],"preferred_protocol":"usenet","protocol_delay_minutes":43200}`))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "protocol_delay_minutes") {
		t.Fatalf("status = %d (%s), want 400 naming protocol_delay_minutes", recorder.Code, recorder.Body)
	}
}

// "The items in the library should also show if a file satisfies the target or cutoff."
func TestListWorkParts_MarksFilesAgainstTheTargetAndCutoff(t *testing.T) {
	fixture := newWorksFixture(t)
	library, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	ids := profileIDs(t, fixture.cache)
	bitrate := int64(256)
	cutoff := ids["Lossless preferred"]
	if err := fixture.cache.SetCategoryQuality(t.Context(), library.ID,
		[]int64{ids["Lossless preferred"], ids["Lossy 320"]}, &cutoff,
		aimTarget(t, fixture.cache, "music", settings.QualityTarget{AudioCodec: "aac", Container: "m4a", BitrateKbps: &bitrate, TolerancePct: 10})); err != nil {
		t.Fatal(err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	file := func(codec, ext string, kbps int64, lossless bool) *apiserver.PartFileRow {
		return &apiserver.PartFileRow{MediaObjectID: 1, AudioCodec: codec, Container: "x", Extension: ext,
			BitrateKbps: kbps, Lossless: lossless}
	}
	fixture.workDetail.row = apiserver.WorkDetailRow{
		ID: 1, MediaType: "music", Title: "Album", SortTitle: "album", CategoryIDs: []int64{library.ID},
	}
	fixture.workDetail.parts = []apiserver.PartRow{
		{ID: 1, MediaRef: "disc1:track1", File: file("aac", "m4a", 270, false)},  // within 10% of 256
		{ID: 2, MediaRef: "disc1:track2", File: file("aac", "m4a", 128, false)},  // too low
		{ID: 3, MediaRef: "disc1:track3", File: file("flac", "flac", 900, true)}, // the cutoff profile
		{ID: 4, MediaRef: "disc1:track4", File: file("", "", 0, false)},          // not probed yet
		{ID: 5, MediaRef: "disc1:track5", File: file("mp3", "mp3", 320, false)},  // fits only after the cutoff
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/parts"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Parts []struct {
			File *struct {
				MeetsTarget *bool `json:"meets_target"`
				MeetsCutoff *bool `json:"meets_cutoff"`
			} `json:"file"`
		} `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	flag := func(b *bool) string {
		if b == nil {
			return "-"
		}
		return strconv.FormatBool(*b)
	}
	var got []string
	for _, part := range body.Parts {
		got = append(got, flag(part.File.MeetsTarget)+"/"+flag(part.File.MeetsCutoff))
	}
	want := []string{"true/true", "false/false", "false/true", "-/-", "false/false"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("target/cutoff per file = %v, want %v", got, want)
	}
}

// aimTarget stores a target under a fresh name and answers its id, the way a library picks one.
func aimTarget(t *testing.T, cache *settings.Cache, mediaType string, target settings.QualityTarget) *int64 {
	t.Helper()
	target.Name, target.MediaType = fmt.Sprintf("%s %d", t.Name(), time.Now().UnixNano()), mediaType
	created, err := cache.CreateQualityTarget(t.Context(), target)
	if err != nil {
		t.Fatalf("CreateQualityTarget: %v", err)
	}
	return &created.ID
}
