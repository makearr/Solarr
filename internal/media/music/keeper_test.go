package music_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/music"
)

// A flac and a 96 kbit mp3 of one song: the library decides which is closer to what it wants.
func TestChooseKeeper_TheCopyCloserToTheLibrarysTargetWins(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Artist", "Album", 2001, alwaysAcceptRules)
	flac := music.KeeperFile{AudioCodec: "flac", Container: "flac", Lossless: true, BitrateKbps: 900, Path: "A/01.flac"}
	mp3 := music.KeeperFile{AudioCodec: "mp3", Container: "mp3", BitrateKbps: 96, Path: "B/01.mp3"}

	if err := env.settings.SetCategoryQuality(t.Context(), item.CategoryID, []int64{item.ProfileID}, nil,
		&settings.QualityTarget{AudioCodec: "flac", Container: "flac"}); err != nil {
		t.Fatalf("SetCategoryQuality: %v", err)
	}
	keeper, reason := music.ChooseKeeper(env.settings.Snapshot(), item.CategoryID, []music.KeeperFile{mp3, flac})
	if keeper != 1 || reason == "" {
		t.Errorf("target flac: keeper %d (%q), want the flac at index 1 with a reason", keeper, reason)
	}

	bitrate := int64(96)
	if err := env.settings.SetCategoryQuality(t.Context(), item.CategoryID, []int64{item.ProfileID}, nil,
		&settings.QualityTarget{AudioCodec: "mp3", Container: "mp3", BitrateKbps: &bitrate, TolerancePct: 10}); err != nil {
		t.Fatalf("SetCategoryQuality: %v", err)
	}
	if keeper, _ := music.ChooseKeeper(env.settings.Snapshot(), item.CategoryID, []music.KeeperFile{flac, mp3}); keeper != 1 {
		t.Errorf("target mp3 96 kbit: keeper %d, want the mp3 that is exactly it", keeper)
	}
}
