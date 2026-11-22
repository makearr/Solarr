package settings_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// shippedProfileIDs answers the shipped profiles by name.
func shippedProfileIDs(t *testing.T, cache *settings.Cache) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	for _, profile := range cache.Snapshot().QualityProfiles() {
		ids[profile.Name] = profile.ID
	}
	return ids
}

// The user's own example order leads the list of every profile (migration 0019).
func TestQualityProfiles_ListedInSuggestedOrder(t *testing.T) {
	cache, _ := newCache(t)
	var names []string
	for _, profile := range cache.Snapshot().QualityProfiles() {
		if profile.MediaType == "music" {
			names = append(names, profile.Name)
		}
	}
	want := []string{"Space conscious", "Lossy 320", "Lossless preferred"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

func TestSetCategoryQuality_OrderCutoffAndTarget(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	ids := shippedProfileIDs(t, cache)
	list := []int64{ids["Lossy 320"], ids["Lossless preferred"]}
	bitrate := int64(256)
	target, err := cache.CreateQualityTarget(t.Context(), settings.QualityTarget{
		Name: "AAC 256", MediaType: "music", AudioCodec: "aac", Container: "m4a", BitrateKbps: &bitrate, TolerancePct: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := cache.SetCategoryQuality(t.Context(), music.ID, list, &list[1], &target.ID); err != nil {
		t.Fatalf("SetCategoryQuality: %v", err)
	}
	got, _ := cache.Snapshot().Category(music.ID)
	if !reflect.DeepEqual(got.QualityProfileIDs, list) || *got.QualityProfileID != list[0] {
		t.Errorf("list = %v first = %v, want %v with the first as the library's profile",
			got.QualityProfileIDs, got.QualityProfileID, list)
	}
	if got.QualityCutoffID == nil || *got.QualityCutoffID != list[1] || got.QualityTarget == nil || !reflect.DeepEqual(*got.QualityTarget, target) {
		t.Errorf("cutoff = %v target = %+v", got.QualityCutoffID, got.QualityTarget)
	}

	// Choosing one profile the old way puts it first and keeps the rest of the list.
	if err := cache.SetCategoryQualityProfile(t.Context(), music.ID, &list[1]); err != nil {
		t.Fatal(err)
	}
	got, _ = cache.Snapshot().Category(music.ID)
	if want := []int64{list[1], list[0]}; !reflect.DeepEqual(got.QualityProfileIDs, want) {
		t.Errorf("after choosing one: %v, want %v", got.QualityProfileIDs, want)
	}
}

func TestSetCategoryQuality_ReportsEveryProblem(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	ids := shippedProfileIDs(t, cache)
	outside := ids["Lossless preferred"]
	missing := int64(999)

	err := cache.SetCategoryQuality(t.Context(), music.ID,
		[]int64{ids["Lossy 320"], ids["Lossy 320"], 999}, &outside, &missing)
	var problems config.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("err = %v, want config.Problems", err)
	}
	var paths []string
	for _, problem := range problems {
		paths = append(paths, problem.Path)
	}
	want := []string{"profiles[1]", "profiles[2]", "cutoff", "target"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("problems at %v, want %v", paths, want)
	}
}

func TestCreateQualityTarget_ReportsEveryProblemAndAMissingName(t *testing.T) {
	cache, _ := newCache(t)
	zero := int64(0)
	_, err := cache.CreateQualityTarget(t.Context(), settings.QualityTarget{
		MediaType: "music", Container: "m4a", BitrateKbps: &zero, TolerancePct: 80, CRF: 70,
	})
	var problems config.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("err = %v, want config.Problems", err)
	}
	var paths []string
	for _, problem := range problems {
		paths = append(paths, problem.Path)
	}
	want := []string{"target.name", "target.audio_codec", "target.bitrate_kbps", "target.crf", "target.tolerance_pct"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("problems at %v, want %v", paths, want)
	}
}

func TestDeleteQualityTarget_RefusedWhileALibraryAimsForIt(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	target, err := cache.CreateQualityTarget(t.Context(), settings.QualityTarget{Name: "FLAC", MediaType: "music", AudioCodec: "flac", Container: "flac"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetCategoryQuality(t.Context(), music.ID, nil, nil, &target.ID); err != nil {
		t.Fatal(err)
	}
	if err := cache.DeleteQualityTarget(t.Context(), target.ID); !errors.Is(err, settings.ErrInUse) {
		t.Fatalf("delete in use: err = %v, want ErrInUse", err)
	}
	if err := cache.SetCategoryQuality(t.Context(), music.ID, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.DeleteQualityTarget(t.Context(), target.ID); err != nil {
		t.Fatalf("delete unused: %v", err)
	}
}

func TestDeleteQualityProfile_RefusedWhileAnyLibraryListsIt(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	ids := shippedProfileIDs(t, cache)
	if err := cache.SetCategoryQuality(t.Context(), music.ID,
		[]int64{ids["Lossy 320"], ids["Space conscious"]}, nil, nil); err != nil {
		t.Fatal(err)
	}
	var inUse *settings.InUseError
	if err := cache.DeleteQualityProfile(t.Context(), ids["Space conscious"]); !errors.As(err, &inUse) {
		t.Errorf("deleting a fallback profile: err = %v, want InUseError", err)
	} else if want := `settings: quality profile "Space conscious" is still referenced by 1 categories`; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("message = %q, want it to start with %q", err.Error(), want)
	}
}

func TestDocument_RoundTrips_IncludesLibraryQuality(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	ids := shippedProfileIDs(t, cache)
	list := []int64{ids["Space conscious"], ids["Lossy 320"]}
	flac, err := cache.CreateQualityTarget(t.Context(), settings.QualityTarget{Name: "FLAC", MediaType: "music", AudioCodec: "flac", Container: "flac"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetCategoryQuality(t.Context(), music.ID, list, &list[1], &flac.ID); err != nil {
		t.Fatal(err)
	}

	exported := cache.Export()
	encoded, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	var decoded settings.Document
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	fresh, freshClock := newCache(t)
	if err := fresh.Import(t.Context(), freshClock, netip.Addr{}, decoded); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := fresh.Export(); !equalDocuments(got, exported) {
		t.Errorf("round trip changed the configuration:\n got %+v\nwant %+v", got, exported)
	}
}

// A document from before libraries held a list still imports, as a list of one.
func TestImport_ASingleQualityProfileBecomesAListOfOne(t *testing.T) {
	source, _ := newCache(t)
	mustCategory(t, source, "music", "Music", 10)
	document := source.Export()
	document.Categories[0].QualityProfile = "Lossy 320"
	document.Categories[0].QualityProfiles = nil

	cache, cl := newCache(t)
	if err := cache.Import(t.Context(), cl, netip.Addr{}, document); err != nil {
		t.Fatalf("import: %v", err)
	}
	category := cache.Snapshot().Categories()[0]
	if want := []int64{shippedProfileIDs(t, cache)["Lossy 320"]}; !reflect.DeepEqual(category.QualityProfileIDs, want) {
		t.Errorf("list = %v, want %v", category.QualityProfileIDs, want)
	}
}

// A library lists only profiles of its own media type (2026-09-25).
func TestSetCategoryQuality_RefusesAnotherMediaTypesProfile(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	tv, err := cache.CreateQualityProfileFor(t.Context(), "tv", "HD", `{"quality_profile":"x","compare":["score"],"tiers":{},"targets":{}}`, "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfileFor: %v", err)
	}
	if tv.MediaType != "tv" {
		t.Errorf("MediaType = %q, want tv", tv.MediaType)
	}
	err = cache.SetCategoryQuality(t.Context(), music.ID, []int64{tv.ID}, nil, nil)
	var problems config.Problems
	if !errors.As(err, &problems) || len(problems) != 1 || problems[0].Expectation != "a music quality profile" {
		t.Errorf("err = %v, want one problem asking for a music profile", err)
	}
	if _, err := cache.CreateQualityProfileFor(t.Context(), "vinyl", "x", `{"quality_profile":"x","compare":["score"],"tiers":{},"targets":{}}`, "{}"); err == nil {
		t.Error("created a profile for an unknown media type")
	}
}

func TestSetCategoryProtocolDelay_ValidatesAndSurvivesAnExportImport(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	if err := cache.SetCategoryProtocolDelay(t.Context(), music.ID, "gopher", 5); err == nil {
		t.Fatal("an unknown protocol was accepted")
	}
	if err := cache.SetCategoryProtocolDelay(t.Context(), music.ID, "usenet", 8*24*60); err == nil {
		t.Fatal("a delay of over a week was accepted")
	}
	if err := cache.SetCategoryProtocolDelay(t.Context(), music.ID, "usenet", 120); err != nil {
		t.Fatalf("SetCategoryProtocolDelay: %v", err)
	}
	got, _ := cache.Snapshot().Category(music.ID)
	if got.PreferredProtocol != "usenet" || got.ProtocolDelayMinutes != 120 {
		t.Fatalf("category = %+v, want usenet and 120", got)
	}

	exported := cache.Export()
	encoded, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	var decoded settings.Document
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	fresh, freshClock := newCache(t)
	if err := fresh.Import(t.Context(), freshClock, netip.Addr{}, decoded); err != nil {
		t.Fatalf("import: %v", err)
	}
	var imported settings.Category
	for _, category := range fresh.Snapshot().Categories() {
		if category.Name == "Music" {
			imported = category
		}
	}
	if imported.PreferredProtocol != "usenet" || imported.ProtocolDelayMinutes != 120 {
		t.Errorf("imported %+v, want the delay profile back", imported)
	}
}

// A document written before targets were named holds the target inside the category; it is read as a target named after the library.
func TestDocument_ReadsALegacyInlineTargetAsANamedOne(t *testing.T) {
	cache, _ := newCache(t)
	source := `version: 1
categories:
  - media_type: music
    name: Music
    priority_rank: 10
    quality_target:
      audio_codec: flac
      container: flac
      tolerance_pct: 10
`
	var document settings.Document
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		t.Fatal(err)
	}
	if problems := document.Validate(netip.Addr{}); len(problems) > 0 {
		t.Fatalf("legacy document does not validate: %v", problems)
	}
	if err := cache.Import(t.Context(), clock.NewFakeAtEpoch(), netip.Addr{}, document); err != nil {
		t.Fatalf("import: %v", err)
	}
	var found *settings.QualityTarget
	for _, category := range cache.Snapshot().Categories() {
		if category.Name == "Music" {
			found = category.QualityTarget
		}
	}
	if found == nil || found.Name != "Music target" || found.AudioCodec != "flac" {
		t.Fatalf("category target = %+v, want the library's own target named Music target", found)
	}
}

func TestShippedTargets_EveryFamilyHasOneSpaceConsciousPresetAndAllValidate(t *testing.T) {
	cache, _ := newCache(t)
	labelled := map[string]int{}
	for _, target := range cache.Snapshot().QualityTargets() {
		if !target.Builtin {
			t.Errorf("target %q is not marked built in", target.Name)
		}
		if target.Label == "Space conscious" {
			labelled[target.MediaType]++
		}
		if err := cache.UpdateQualityTarget(t.Context(), target); err != nil {
			t.Errorf("shipped target %q does not validate: %v", target.Name, err)
		}
	}
	want := map[string]int{"video": 1, "music": 1, "spoken": 1, "book": 1}
	if !reflect.DeepEqual(labelled, want) {
		t.Errorf("space conscious presets = %v, want %v", labelled, want)
	}
}

func TestShippedTargetProfiles_EachRecommendsATargetOfEveryMediaTypeItServes(t *testing.T) {
	cache, _ := newCache(t)
	snapshot := cache.Snapshot()
	if got := len(snapshot.TargetProfiles()); got != 12 {
		t.Fatalf("shipped content profiles = %d, want 12", got)
	}
	named := map[string]bool{}
	for _, target := range snapshot.QualityTargets() {
		named[target.MediaType+"\x00"+target.Name] = true
	}
	for _, profile := range snapshot.TargetProfiles() {
		for _, mediaType := range profile.MediaTypes {
			if !named[mediaType+"\x00"+profile.RecommendedTarget] {
				t.Errorf("profile %q recommends %q, which %s has not", profile.Name, profile.RecommendedTarget, mediaType)
			}
		}
	}
}

func TestTargetProfile_CreateUpdateAndDeleteKeepTheTargetsTunedForIt(t *testing.T) {
	cache, _ := newCache(t)
	created, err := cache.CreateTargetProfile(t.Context(), settings.TargetProfile{Name: "Documentary", MediaTypes: []string{"tv"}, Resolution: "1080p", CRF: 25})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.CreateTargetProfile(t.Context(), settings.TargetProfile{Name: "documentary", MediaTypes: []string{"tv"}}); err == nil {
		t.Error("a second profile with the same name was accepted")
	}
	if _, err := cache.CreateTargetProfile(t.Context(), settings.TargetProfile{Name: "Empty"}); err == nil {
		t.Error("a profile for no media type was accepted")
	}
	target, err := cache.CreateQualityTarget(t.Context(), settings.QualityTarget{Name: "Doc target", MediaType: "tv", Resolution: "1080p", ProfileID: &created.ID, EncoderTune: "film", Deinterlace: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.DeleteTargetProfile(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	kept, found := cache.Snapshot().QualityTarget(target.ID)
	if !found || kept.ProfileID != nil || kept.EncoderTune != "film" || !kept.Deinterlace {
		t.Errorf("target after its profile was deleted = %+v, want it kept without the link", kept)
	}
}

func TestShippedVideoProfiles_CarryNoSizeLimitsUnlessAskedFor(t *testing.T) {
	cache, _ := newCache(t)
	limited := 0
	for _, profile := range cache.Snapshot().QualityProfiles() {
		rules, err := score.Parse([]byte(profile.RulesJSON))
		if err != nil {
			t.Fatalf("profile %q no longer parses: %v", profile.Name, err)
		}
		if rules.Require.Size == "reject_outside_range" && len(rules.Targets["overall_bitrate"]) > 0 {
			limited++
		}
	}
	if limited != 0 {
		t.Errorf("profiles with size limits = %d, want none: they are opt-in", limited)
	}
}

func TestTargetFamilies_MovieSeriesAndAdultLibrariesShareOneSetOfPresetsAndEachPicksItsOwn(t *testing.T) {
	cache, _ := newCache(t)
	snapshot := cache.Snapshot()
	names := map[string]int{}
	for _, target := range snapshot.QualityTargets() {
		names[target.MediaType+"\x00"+target.Name]++
	}
	for key, count := range names {
		if count != 1 {
			t.Errorf("%q is stored %d times", key, count)
		}
	}
	var preferred settings.QualityTarget
	for _, target := range snapshot.QualityTargets() {
		if target.MediaType == "video" && target.Label == settings.PreferredTargetLabel {
			preferred = target
		}
	}
	movies, _ := cache.CreateCategory(t.Context(), "movie", "Movies", 10)
	series, _ := cache.CreateCategory(t.Context(), "tv", "Series", 10)
	music, _ := cache.CreateCategory(t.Context(), "music", "Music", 10)
	for _, library := range []settings.Category{movies, series} {
		if err := cache.SetCategoryQuality(t.Context(), library.ID, nil, nil, &preferred.ID); err != nil {
			t.Errorf("library of type %s refused a video target: %v", library.MediaType, err)
		}
	}
	if err := cache.SetCategoryQuality(t.Context(), music.ID, nil, nil, &preferred.ID); err == nil {
		t.Error("a music library accepted a video target")
	}
	if created, err := cache.CreateQualityTarget(t.Context(), settings.QualityTarget{Name: "Mine", MediaType: "tv", Resolution: "720p"}); err != nil || created.MediaType != "video" {
		t.Errorf("a target made for tv = %+v, %v; want it in the video family", created, err)
	}
}

// Every profile that ships passes the strict check a user's own profile must pass.
func TestQualityProfiles_ShippedPassStrictCheck(t *testing.T) {
	cache, _ := newCache(t)
	for _, profile := range cache.Snapshot().QualityProfiles() {
		if problems := score.ValidateStrict([]byte(profile.RulesJSON)); len(problems) > 0 {
			t.Errorf("%s: %v", profile.Name, problems)
		}
	}
}

// A mistyped target is refused on save with the field named (QUAL-4).
func TestCreateQualityProfile_RefusesMistypedTarget(t *testing.T) {
	cache, _ := newCache(t)
	rules := `{"compare":["bitrate_distance"],"targets":{"bitrate":[{"ideal":"20 Mb/s"}]}}`
	_, err := cache.CreateQualityProfile(context.Background(), "Typo", rules, `{}`)
	var problems config.Problems
	if !errors.As(err, &problems) || len(problems) != 1 || problems[0].Path != "rules.targets.bitrate[0].ideal" {
		t.Fatalf("err = %#v", err)
	}
}

// PERF-11: one parse per snapshot however often the profile is asked for, and a new snapshot sees an edited profile.
func TestSnapshot_ParsesAProfileOncePerSnapshot(t *testing.T) {
	cache, _ := newCache(t)
	profile, err := cache.CreateQualityProfile(context.Background(), "Parsed once", `{"compare":["score"]}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := cache.Snapshot()
	first, err := snapshot.ParsedQualityProfile(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := snapshot.ParsedQualityProfile(profile.ID)
	if &first.Compare[0] != &second.Compare[0] {
		t.Error("the second call parsed the rules again")
	}
	if err := cache.UpdateQualityProfile(context.Background(), profile.ID, "Parsed once", `{"compare":["score","lossless"]}`, `{}`); err != nil {
		t.Fatal(err)
	}
	if edited, _ := cache.Snapshot().ParsedQualityProfile(profile.ID); len(edited.Compare) != 2 {
		t.Errorf("a new snapshot parsed %v, want the edited rules", edited.Compare)
	}
	if _, err := snapshot.ParsedQualityProfile(profile.ID + 1000); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("unknown profile: %v", err)
	}
}

type usageStub struct {
	files, recycled, queued, onRoot, profileItems, targetItems int64
	repointed                                                  *[][2]int64
}

func (u usageStub) RepointProfile(_ context.Context, from, to int64) error {
	*u.repointed = append(*u.repointed, [2]int64{from, to})
	return nil
}
func (u usageStub) RepointTarget(context.Context, int64, int64) error { return nil }

func (u usageStub) RootFolderRows(context.Context, int64) (int64, int64, int64, error) {
	return u.files, u.recycled, u.queued, nil
}
func (u usageStub) CategoryFilesOnRoot(context.Context, int64, int64) (int64, error) {
	return u.onRoot, nil
}
func (u usageStub) ItemsPinnedToProfile(context.Context, int64) (int64, error) {
	return u.profileItems, nil
}
func (u usageStub) ItemsPinnedToTarget(context.Context, int64) (int64, error) {
	return u.targetItems, nil
}

// DB-16: settings rows that library rows still name are not deleted.
func TestDeletes_AskTheLibraryFirst(t *testing.T) {
	cache, _ := newCache(t)
	ctx := context.Background()
	profile, err := cache.CreateQualityProfile(ctx, "Pinned", `{"compare":["score"]}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	cache.EnableLibraryUsage(usageStub{profileItems: 3, repointed: new([][2]int64)})
	var inUse *settings.InUseError
	err = cache.DeleteQualityProfile(ctx, profile.ID)
	want := []settings.Reference{{Kind: "items pinned to it", Count: 3}}
	if !errors.As(err, &inUse) || !reflect.DeepEqual(inUse.References, want) {
		t.Fatalf("pinned profile: %v, want references %v", err, want)
	}
	cache.EnableLibraryUsage(usageStub{repointed: new([][2]int64)})
	if err := cache.DeleteQualityProfile(ctx, profile.ID); err != nil {
		t.Fatalf("unpinned profile: %v", err)
	}

	root, err := cache.CreateRootFolder(ctx, t.TempDir(), "usage")
	if err != nil {
		t.Fatal(err)
	}
	cache.EnableLibraryUsage(usageStub{files: 2, recycled: 1, repointed: new([][2]int64)})
	err = cache.DeleteRootFolder(ctx, root.ID)
	want = []settings.Reference{{Kind: "files in the library", Count: 2}, {Kind: "recycle bin entries", Count: 1}}
	if !errors.As(err, &inUse) || !reflect.DeepEqual(inUse.References, want) {
		t.Fatalf("root with files: %v, want references %v", err, want)
	}
	cache.EnableLibraryUsage(usageStub{onRoot: 4, repointed: new([][2]int64)})
	if err := cache.DetachRootFolder(ctx, 1, root.ID); !errors.As(err, &inUse) {
		t.Fatalf("detach with files: %v", err)
	}
}

// SEC-17: a merged tag's block follows the tag it became, and a tag an account is kept from is known.
func TestMoveUserBlockedTag_TheBlockFollowsTheMerge(t *testing.T) {
	cache, fake := newCache(t)
	ctx := context.Background()
	user, err := cache.CreateUser(ctx, fake, "kid", "hash", settings.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetUserBlockedTags(ctx, user.ID, []settings.UserBlockedTag{{TagID: 5, Name: "Horror"}}); err != nil {
		t.Fatal(err)
	}
	if !cache.Snapshot().TagBlockedForAnyUser(5) || cache.Snapshot().TagBlockedForAnyUser(6) {
		t.Fatal("tag 5 is blocked and tag 6 is not")
	}
	if err := cache.MoveUserBlockedTag(ctx, 5, 6, "Scary"); err != nil {
		t.Fatal(err)
	}
	want := []settings.UserBlockedTag{{TagID: 6, Name: "Scary"}}
	if got := cache.Snapshot().UserBlockedTags(user.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("blocks = %+v, want %+v", got, want)
	}
}

// SEC-8: a preference has a bounded key and value, and an account a bounded number of them.
func TestSetUserPreference_IsBounded(t *testing.T) {
	cache, fake := newCache(t)
	ctx := context.Background()
	user, err := cache.CreateUser(ctx, fake, "kid", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "Upper", "1start", "has space", strings.Repeat("a", 97)} {
		if err := cache.SetUserPreference(ctx, user.ID, key, `1`); err == nil {
			t.Errorf("key %q was accepted", key)
		}
	}
	if err := cache.SetUserPreference(ctx, user.ID, "list.view.works", `"`+strings.Repeat("x", 8<<10)+`"`); err == nil {
		t.Error("a value over 8 KiB was accepted")
	}
	for index := range 200 {
		if err := cache.SetUserPreference(ctx, user.ID, fmt.Sprintf("k%d", index), `1`); err != nil {
			t.Fatalf("preference %d: %v", index, err)
		}
	}
	if err := cache.SetUserPreference(ctx, user.ID, "one.more", `1`); err == nil {
		t.Error("the 201st preference was accepted")
	}
	if err := cache.SetUserPreference(ctx, user.ID, "k0", `2`); err != nil {
		t.Errorf("updating a kept key: %v", err)
	}
}

// USER-1: an account's parts change together or not at all, and two creations of one name give one account.
func TestUpdateAccount_RefusalChangesNothingAndANameIsTakenOnce(t *testing.T) {
	cache, fake := newCache(t)
	ctx := context.Background()
	if _, err := cache.CreateUser(ctx, fake, "boss", "hash", settings.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	kid, err := cache.CreateUser(ctx, fake, "kid", "hash", settings.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	role, bad, hashed := settings.RoleReadonly, settings.UserLimits{TierDefault: "nonsense"}, "newhash"
	err = cache.UpdateAccount(ctx, kid.ID, settings.AccountPatch{Role: &role, Limits: &bad, PasswordHash: &hashed})
	var problems config.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("invalid limits: %v, want problems", err)
	}
	if after, _ := cache.Snapshot().User(kid.ID); after.Role != settings.RoleUser || after.SessionStamp != kid.SessionStamp {
		t.Errorf("a refused update changed the account: %+v", after)
	}
	good := settings.UserLimits{TierDefault: "normal", TierMax: "expert", NewLibraryPermission: settings.PermissionRead}
	if err := cache.UpdateAccount(ctx, kid.ID, settings.AccountPatch{Role: &role, Limits: &good, PasswordHash: &hashed}); err != nil {
		t.Fatal(err)
	}
	if after, _ := cache.Snapshot().User(kid.ID); after.Role != settings.RoleReadonly || after.SessionStamp == kid.SessionStamp {
		t.Errorf("a whole update did not apply: %+v", after)
	}

	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := cache.CreateAccount(ctx, fake, settings.NewAccount{Username: "twin", PasswordHash: "h", Role: settings.RoleUser, Limits: cache.DefaultLimits(settings.RoleUser)})
			results <- err
		}()
	}
	first, second := <-results, <-results
	if (first == nil) == (second == nil) || (first != nil && !errors.Is(first, settings.ErrUsernameTaken)) || (second != nil && !errors.Is(second, settings.ErrUsernameTaken)) {
		t.Errorf("two creations of one name: %v and %v, want one success and one ErrUsernameTaken", first, second)
	}
}

func TestUpdateAccount_PromotionTierAndTheLastAdministrator(t *testing.T) {
	cache, fake := newCache(t)
	ctx := context.Background()
	music, _ := cache.CreateCategory(ctx, "music", "Music", 10)
	boss, err := cache.CreateUser(ctx, fake, "boss", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := cache.CreateUser(ctx, fake, "kid", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatal(err)
	}

	demote := settings.RoleUser
	if err := cache.UpdateAccount(ctx, boss.ID, settings.AccountPatch{Role: &demote}); err == nil {
		t.Error("the only administrator was demoted")
	}
	if after, _ := cache.Snapshot().User(boss.ID); after.Role != settings.RoleAdmin {
		t.Errorf("a refused demotion changed the role to %q", after.Role)
	}

	promote := settings.RoleAdmin
	if err := cache.UpdateAccount(ctx, kid.ID, settings.AccountPatch{Role: &promote}); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().Permission(kid.ID, music.ID); got != settings.PermissionManage {
		t.Errorf("permission after promotion = %q, want manage", got)
	}

	limits := settings.UserLimits{TierDefault: settings.TierExpert, TierMax: settings.TierExpert, NewLibraryPermission: settings.PermissionRead}
	if err := cache.UpdateAccount(ctx, kid.ID, settings.AccountPatch{Limits: &limits}); err != nil {
		t.Fatal(err)
	}
	if got, _ := cache.Snapshot().User(kid.ID); got.Tier != settings.TierExpert {
		t.Errorf("tier after a default-tier change = %q, want expert", got.Tier)
	}
}

func TestDeleteRootFolder_ForgetsItsPlacementChoicesInsteadOfFailingOnThem(t *testing.T) {
	cache, _ := newCache(t)
	ctx := context.Background()
	category, _ := cache.CreateCategory(ctx, "music", "Music", 10)
	root, err := cache.CreateRootFolder(ctx, t.TempDir(), "once used")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetPlacementAffinity(ctx, category.ID, "artist:1", root.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := cache.DeleteRootFolder(ctx, root.ID); err != nil {
		t.Fatalf("DeleteRootFolder = %v, want the root gone with its remembered placements", err)
	}
	if _, found := cache.Snapshot().RootFolder(root.ID); found {
		t.Error("the root is still there")
	}
}

// DB-16: a profile in use is deleted when its items and import lists are moved to another profile of its type.
func TestReplaceQualityProfile_MovesItsUsersThenDeletes(t *testing.T) {
	cache, _ := newCache(t)
	ctx := context.Background()
	from, err := cache.CreateQualityProfile(ctx, "From", `{"compare":["score"]}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	to, err := cache.CreateQualityProfile(ctx, "To", `{"compare":["score"]}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var repointed [][2]int64
	cache.EnableLibraryUsage(usageStub{repointed: &repointed})
	if err := cache.ReplaceQualityProfile(ctx, from.ID, from.ID); !errors.Is(err, settings.ErrNotFound) {
		t.Fatalf("replaced by itself: %v, want refusal", err)
	}
	if err := cache.ReplaceQualityProfile(ctx, from.ID, to.ID); err != nil {
		t.Fatalf("ReplaceQualityProfile: %v", err)
	}
	if want := [][2]int64{{from.ID, to.ID}}; !reflect.DeepEqual(repointed, want) {
		t.Errorf("items repointed %v, want %v", repointed, want)
	}
	if _, found := cache.Snapshot().QualityProfile(from.ID); found {
		t.Error("the replaced profile still exists")
	}
}
