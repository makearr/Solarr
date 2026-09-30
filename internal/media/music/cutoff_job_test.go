package music_test

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

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
		&settings.QualityTarget{AudioCodec: "flac", Container: "flac", TolerancePct: 10}); err != nil {
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
}
