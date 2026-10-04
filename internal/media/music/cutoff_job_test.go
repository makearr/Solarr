package music_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
)

// M1d: an album with all its tracks whose file falls short of the library's quality is Lidarr's
// "cutoff unmet" (UPGRADABLE); measured again at the target, it is complete.
func TestCutoffJob_MarksAnAlbumBelowTheCutoffUpgradable(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	if err := env.settings.SetCategoryQuality(t.Context(), item.CategoryID, []int64{item.ProfileID}, nil,
		aimTarget(t, env.settings, "music", settings.QualityTarget{AudioCodec: "flac", Container: "flac", TolerancePct: 10})); err != nil {
		t.Fatalf("SetCategoryQuality: %v", err)
	}
	physical := library.NewPhysical(env.library)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Nosi Zada/Discovery/01.flac", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	object := registered[0].MediaObjectID
	if err := env.library.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).AttachFile(ctx, librarydb.AttachFileParams{
			DesiredItemID: item.ID, MediaObjectID: object, Role: "primary", MediaRef: "1", AddedAt: 1,
		})
	}); err != nil {
		t.Fatalf("AttachFile: %v", err)
	}
	desired := library.NewDesired(env.library)
	if err := desired.SetAlbumState(t.Context(), item.ID, "COMPLETE", 1); err != nil {
		t.Fatalf("SetAlbumState: %v", err)
	}
	job := music.CutoffJob{Desired: desired, Settings: env.settings, Logger: slog.New(slog.DiscardHandler)}
	measure := func(codec string) {
		t.Helper()
		if err := physical.RecordMeasured(t.Context(), object, library.AttributeClaim{AudioCodec: codec, Container: codec},
			library.MeasuredScalars{AudioBitrate: 900_000, OverallBitrate: 900_000}, "", 1); err != nil {
			t.Fatalf("RecordMeasured: %v", err)
		}
	}
	stateAfterRun := func() string {
		t.Helper()
		if err := job.Run(t.Context(), jobs.Job{Type: music.CutoffJobType}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		got, err := desired.Get(t.Context(), item.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		return got.State
	}

	if state := stateAfterRun(); state != "COMPLETE" {
		t.Fatalf("unmeasured: state %s, want it left COMPLETE", state)
	}
	measure("mp3")
	if state := stateAfterRun(); state != "UPGRADABLE" {
		t.Fatalf("an mp3 against a flac target: state %s, want UPGRADABLE", state)
	}
	measure("flac")
	if state := stateAfterRun(); state != "COMPLETE" {
		t.Fatalf("measured as flac: state %s, want COMPLETE again", state)
	}
	// Upgrades off (Lidarr's "Upgrades Allowed"): the mp3 is enough.
	measure("mp3")
	if err := env.settings.SetCategoryQualityUpgrades(t.Context(), item.CategoryID, false); err != nil {
		t.Fatalf("SetCategoryQualityUpgrades: %v", err)
	}
	if state := stateAfterRun(); state != "COMPLETE" {
		t.Fatalf("upgrades off: state %s, want COMPLETE", state)
	}
}

// An album given a profile of its own is judged against that profile alone: a library with no target would call an
// mp3 enough, the album's own flac-first profile does not.
func TestCutoffJob_AnAlbumsOwnProfileDecidesWhetherItIsUpgradable(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	physical := library.NewPhysical(env.library)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Nosi Zada/Discovery/01.mp3", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	object := registered[0].MediaObjectID
	if err := env.library.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).AttachFile(ctx, librarydb.AttachFileParams{
			DesiredItemID: item.ID, MediaObjectID: object, Role: "primary", MediaRef: "1", AddedAt: 1,
		})
	}); err != nil {
		t.Fatalf("AttachFile: %v", err)
	}
	desired := library.NewDesired(env.library)
	if err := desired.SetAlbumState(t.Context(), item.ID, "COMPLETE", 1); err != nil {
		t.Fatalf("SetAlbumState: %v", err)
	}
	if err := physical.RecordMeasured(t.Context(), object, library.AttributeClaim{AudioCodec: "mp3", Container: "mp3"},
		library.MeasuredScalars{AudioBitrate: 320_000, OverallBitrate: 320_000}, "", 1); err != nil {
		t.Fatalf("RecordMeasured: %v", err)
	}
	flacFirst, err := env.settings.CreateQualityProfile(t.Context(), "flac-first",
		`{"quality_profile":"flac-first","compare":["audio_codec","score"],"tiers":{"audio_codec":[["flac"],["mp3"]]}}`, "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	job := music.CutoffJob{Desired: desired, Settings: env.settings, Logger: slog.New(slog.DiscardHandler)}
	stateAfterRun := func() string {
		t.Helper()
		if err := job.Run(t.Context(), jobs.Job{Type: music.CutoffJobType}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		got, err := desired.Get(t.Context(), item.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		return got.State
	}

	if state := stateAfterRun(); state != "COMPLETE" {
		t.Fatalf("no profile of its own and no library target: state %s, want COMPLETE", state)
	}
	if err := desired.SetProfileOverride(t.Context(), item.ID, &flacFirst.ID); err != nil {
		t.Fatalf("SetProfileOverride: %v", err)
	}
	if state := stateAfterRun(); state != "UPGRADABLE" {
		t.Fatalf("an mp3 under a flac-first profile of its own: state %s, want UPGRADABLE", state)
	}
	if err := desired.SetProfileOverride(t.Context(), item.ID, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if state := stateAfterRun(); state != "COMPLETE" {
		t.Fatalf("back to the library's profile: state %s, want COMPLETE", state)
	}
}

// M1d: searching an album below its cutoff offers only releases strictly better than the files it has;
// an equal one is refused as no upgrade (Invariant 72: ties keep the incumbent).
func TestSearchItem_AnUpgradableAlbumAcceptsOnlyABetterRelease(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	physical := library.NewPhysical(env.library)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Nosi Zada/Discovery/01.mp3", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if err := env.library.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).AttachFile(ctx, librarydb.AttachFileParams{
			DesiredItemID: item.ID, MediaObjectID: registered[0].MediaObjectID, Role: "primary", MediaRef: "1", AddedAt: 1,
		})
	}); err != nil {
		t.Fatalf("AttachFile: %v", err)
	}
	if err := physical.RecordMeasured(t.Context(), registered[0].MediaObjectID,
		library.AttributeClaim{AudioCodec: "mp3", Container: "mp3"},
		library.MeasuredScalars{AudioBitrate: 320_000, OverallBitrate: 320_000}, "", 1); err != nil {
		t.Fatalf("RecordMeasured: %v", err)
	}
	if err := library.NewDesired(env.library).SetAlbumState(t.Context(), item.ID, music.ItemStateUpgradable, 1); err != nil {
		t.Fatalf("SetAlbumState: %v", err)
	}
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{
			rawItem("guid-mp3", "Nosi Zada-Discovery-2001-MP3-320-GROUP"),
			rawItem("guid-flac", "Nosi Zada-Discovery-2001-FLAC-GROUP"),
		}, nil
	}}

	result, err := env.search().SearchItem(t.Context(), item.ID,
		[]music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchItem: %v", err)
	}
	outcomes := map[string]string{}
	for _, decision := range result.Decisions {
		outcomes[decision.ReleaseCandidate.GUID] = decision.Decision.Outcome
	}
	if outcomes["guid-flac"] != "ACCEPT" || outcomes["guid-mp3"] != "REJECT" {
		t.Errorf("outcomes = %v, want the FLAC accepted and the MP3 320 refused as no upgrade", outcomes)
	}
}

// A complete album wants nothing: every release is refused, as Lidarr's "existing files meet cutoff".
func TestSearchItem_ACompleteAlbumRefusesEveryRelease(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	if err := library.NewDesired(env.library).SetAlbumState(t.Context(), item.ID, music.ItemStateComplete, 1); err != nil {
		t.Fatalf("SetAlbumState: %v", err)
	}
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{rawItem("guid-flac", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
	}}

	result, err := env.search().SearchItem(t.Context(), item.ID,
		[]music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchItem: %v", err)
	}
	if len(result.Decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(result.Decisions))
	}
	got := result.Decisions[0].Decision
	want := `[{"Rule":"upgrade","Field":"quality","Wanted":"files below the cutoff","Got":"the files you have meet it"}]`
	if got.Outcome != "REJECT" || got.RejectionsJSON != want {
		t.Errorf("decision = %s %s, want REJECT %s", got.Outcome, got.RejectionsJSON, want)
	}
}

// replacementAlbum is an album with an owned file and a downloaded one for the same track, both measured.
func replacementAlbum(t *testing.T, ownedCodec, downloadedCodec string) (*searchTestEnv, librarydb.DesiredItem, int64, int64) {
	t.Helper()
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	if err := env.settings.SetCategoryQuality(t.Context(), item.CategoryID, []int64{item.ProfileID}, nil,
		aimTarget(t, env.settings, "music", settings.QualityTarget{AudioCodec: "flac", Container: "flac", TolerancePct: 10})); err != nil {
		t.Fatalf("SetCategoryQuality: %v", err)
	}
	physical := library.NewPhysical(env.library)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Nosi Zada/Discovery/01." + ownedCodec, SizeBytes: 1, QuickHash: "owned", FirstSeenAt: time.Unix(1, 0)},
		{RelPath: "Nosi Zada/Discovery/01 (new)." + downloadedCodec, SizeBytes: 1, QuickHash: "new", FirstSeenAt: time.Unix(2, 0)},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	owned, fromDownload := registered[0].MediaObjectID, registered[1].MediaObjectID
	acquisition, err := library.NewAcquisitions(env.library).Create(t.Context(), library.NewAcquisition{
		DesiredItemID: &item.ID, CandidateGUID: "guid-1", CandidateTitle: "Nosi Zada-Discovery-FLAC", DownloaderID: 1,
		DownloadClientRef: "nzo-1", Protocol: "usenet", State: "IMPORTED", CreatedAt: 1, UpdatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create acquisition: %v", err)
	}
	// The owned file covers track 1; the download is linked to the work and claims track 1, as import
	// leaves a replacement: aligned onto no track, because the track already has a file.
	if err := env.library.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, object := range []int64{owned, fromDownload} {
			if err := queries.LinkWorkFile(ctx, librarydb.LinkWorkFileParams{
				WorkID: item.WorkID, MediaObjectID: object, FirstSeenAt: 1,
			}); err != nil {
				return err
			}
		}
		return queries.AttachFile(ctx, librarydb.AttachFileParams{
			DesiredItemID: item.ID, MediaObjectID: owned, Role: "primary", MediaRef: "disc1:track1", AddedAt: 1,
		})
	}); err != nil {
		t.Fatalf("attach files: %v", err)
	}
	if err := library.NewDesired(env.library).RecordTrackClaims(t.Context(), []library.TrackClaim{
		{MediaObjectID: fromDownload, Disc: 1, Track: 1, Title: "Discovery"},
	}); err != nil {
		t.Fatalf("RecordTrackClaims: %v", err)
	}
	sessions := library.NewImportSessions(env.library)
	sessionID, err := sessions.Create(t.Context(), library.NewSession{
		SourcePath: "/downloads/Nosi Zada-Discovery-FLAC", Origin: "acquisition", AcquisitionID: &acquisition.ID, StartedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	units, err := sessions.AddUnits(t.Context(), sessionID, []library.NewUnit{{RelPath: ".", Shape: "album", FileCount: 1, EvidenceJSON: "{}"}})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	if err := sessions.RecordPlacement(t.Context(), units[0], library.Placement{
		WorkID: item.WorkID, MediaObjectIDs: []int64{fromDownload},
	}); err != nil {
		t.Fatalf("RecordPlacement: %v", err)
	}
	for object, codec := range map[int64]string{owned: ownedCodec, fromDownload: downloadedCodec} {
		if err := physical.RecordMeasured(t.Context(), object, library.AttributeClaim{AudioCodec: codec, Container: codec},
			library.MeasuredScalars{AudioBitrate: 320_000, OverallBitrate: 320_000}, "", 1); err != nil {
			t.Fatalf("RecordMeasured: %v", err)
		}
	}
	if err := library.NewDesired(env.library).SetAlbumState(t.Context(), item.ID, music.ItemStateUpgradable, 1); err != nil {
		t.Fatalf("SetAlbumState: %v", err)
	}
	return env, item, owned, fromDownload
}

// runReplacement runs the one-album check with a recycle that detaches the file, as the real one does.
func runReplacement(t *testing.T, env *searchTestEnv, item librarydb.DesiredItem) map[int64]bool {
	t.Helper()
	desired := library.NewDesired(env.library)
	recycled := map[int64]bool{}
	job := music.CutoffJob{
		Desired: desired, Settings: env.settings, Logger: slog.New(slog.DiscardHandler), Clock: env.clock,
		Recycle: func(ctx context.Context, object, _ int64, replaced bool) error {
			recycled[object] = replaced
			return desired.DetachAndRecompute(ctx, item.ID, item.WorkID, object, []string{"primary"})
		},
	}
	payload, _ := json.Marshal(music.CutoffPayload{WorkID: item.WorkID})
	if err := job.Run(t.Context(), jobs.Job{Type: music.CutoffJobType, Payload: payload}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return recycled
}

// M1d: a downloaded FLAC beats the owned MP3 for the same track -- the MP3 goes to the recycle bin as
// replaced and the album is complete, not cutoff-unmet.
func TestCutoffJob_ADownloadedUpgradeReplacesTheOwnedFile(t *testing.T) {
	env, item, owned, _ := replacementAlbum(t, "mp3", "flac")
	recycled := runReplacement(t, env, item)
	if len(recycled) != 1 || recycled[owned] != true {
		t.Fatalf("recycled %v, want only the owned mp3, as replaced", recycled)
	}
	got, err := library.NewDesired(env.library).Get(t.Context(), item.ID)
	if err != nil || got.State != "COMPLETE" {
		t.Errorf("state = %s, %v; want COMPLETE once the flac replaced the mp3", got.State, err)
	}
}

// A download that measures no better than what is owned is the one recycled (facts win, ties keep
// the incumbent).
func TestCutoffJob_ADownloadNoBetterThanTheOwnedFileGoes(t *testing.T) {
	env, item, _, fromDownload := replacementAlbum(t, "mp3", "mp3")
	recycled := runReplacement(t, env, item)
	if len(recycled) != 1 || recycled[fromDownload] != false {
		t.Fatalf("recycled %v, want only the downloaded mp3, as rejected", recycled)
	}
	if _, found := recycled[fromDownload]; !found {
		t.Fatal("the download that was no better stayed")
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
