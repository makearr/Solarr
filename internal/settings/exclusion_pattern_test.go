package settings_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/settings"
)

func TestExclusionPattern_ShippedDefaultsAreSeeded(t *testing.T) {
	cache, _ := newCache(t)

	patterns := cache.Snapshot().ExclusionPatterns()
	if len(patterns) != 18 {
		t.Fatalf("len(patterns) = %d, want 18 shipped defaults", len(patterns))
	}

	var isoRule *settings.ExclusionPattern
	for i, p := range patterns {
		if !p.Builtin {
			t.Errorf("pattern %q is not marked builtin", p.Pattern)
		}
		if !p.Enabled {
			t.Errorf("pattern %q is not enabled by default", p.Pattern)
		}
		if p.Pattern == "*.iso" {
			isoRule = &patterns[i]
		}
	}
	if isoRule == nil {
		t.Fatal("*.iso pattern not found among shipped defaults")
	}
	if isoRule.Action != settings.ExclusionRefuse {
		t.Errorf("*.iso action = %q, want refuse", isoRule.Action)
	}
	if isoRule.MediaType != "music" {
		t.Errorf("*.iso media_type = %q, want music -- it must not apply to ROMs", isoRule.MediaType)
	}
}

func TestCreateExclusionPattern_RoundTrips(t *testing.T) {
	cache, _ := newCache(t)

	created, err := cache.CreateExclusionPattern(t.Context(), settings.ExclusionPattern{
		Pattern: "*.rar", Action: settings.ExclusionIgnore, Note: "an archive, handled separately",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateExclusionPattern: %v", err)
	}
	if created.Builtin {
		t.Error("a user-created pattern must never be marked builtin")
	}

	found, ok := cache.Snapshot().ExclusionPattern(created.ID)
	if !ok {
		t.Fatal("created pattern not found in snapshot")
	}
	if found.Pattern != "*.rar" || found.Action != settings.ExclusionIgnore {
		t.Errorf("got %+v, want the pattern just created", found)
	}
}

func TestCreateExclusionPattern_RejectsEmptyPattern(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.CreateExclusionPattern(t.Context(), settings.ExclusionPattern{
		Pattern: "", Action: settings.ExclusionIgnore,
	})
	if err == nil {
		t.Fatal("CreateExclusionPattern succeeded, want it refused: pattern is blank")
	}
}

func TestCreateExclusionPattern_RejectsUnknownAction(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.CreateExclusionPattern(t.Context(), settings.ExclusionPattern{
		Pattern: "*.exe", Action: "delete",
	})
	if err == nil {
		t.Fatal("CreateExclusionPattern succeeded, want it refused: action is not ignore or refuse")
	}
}

func TestCreateExclusionPattern_RejectsUnknownMediaType(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.CreateExclusionPattern(t.Context(), settings.ExclusionPattern{
		Pattern: "*.exe", Action: settings.ExclusionRefuse, MediaType: "not-a-real-type",
	})
	if err == nil {
		t.Fatal("CreateExclusionPattern succeeded, want it refused: unknown media type")
	}
}

func TestCreateExclusionPattern_RejectsUnknownCategory(t *testing.T) {
	cache, _ := newCache(t)

	missing := int64(999)
	_, err := cache.CreateExclusionPattern(t.Context(), settings.ExclusionPattern{
		Pattern: "*.exe", Action: settings.ExclusionRefuse, CategoryID: &missing,
	})
	if err == nil {
		t.Fatal("CreateExclusionPattern succeeded, want it refused: category does not exist")
	}
}

func TestCreateExclusionPattern_AcceptsARealCategory(t *testing.T) {
	cache, _ := newCache(t)

	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	created, err := cache.CreateExclusionPattern(t.Context(), settings.ExclusionPattern{
		Pattern: "*.exe", Action: settings.ExclusionRefuse, CategoryID: &category.ID,
	})
	if err != nil {
		t.Fatalf("CreateExclusionPattern: %v", err)
	}
	if created.CategoryID == nil || *created.CategoryID != category.ID {
		t.Errorf("CategoryID = %v, want %d", created.CategoryID, category.ID)
	}
}

func TestUpdateExclusionPattern_ChangesContentButNeverBuiltin(t *testing.T) {
	cache, _ := newCache(t)

	created, err := cache.CreateExclusionPattern(t.Context(), settings.ExclusionPattern{
		Pattern: "*.rar", Action: settings.ExclusionIgnore, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateExclusionPattern: %v", err)
	}

	if err := cache.UpdateExclusionPattern(t.Context(), created.ID, settings.ExclusionPattern{
		Pattern: "*.rar", Action: settings.ExclusionRefuse, Note: "changed my mind", Enabled: false,
	}); err != nil {
		t.Fatalf("UpdateExclusionPattern: %v", err)
	}

	found, ok := cache.Snapshot().ExclusionPattern(created.ID)
	if !ok {
		t.Fatal("updated pattern not found")
	}
	if found.Action != settings.ExclusionRefuse || found.Enabled || found.Note != "changed my mind" {
		t.Errorf("got %+v, want the updated fields", found)
	}
	if found.Builtin {
		t.Error("Update must never turn a pattern builtin")
	}
}

func TestDeleteExclusionPattern_RemovesEvenABuiltinOne(t *testing.T) {
	cache, _ := newCache(t)

	var sfvID int64
	for _, p := range cache.Snapshot().ExclusionPatterns() {
		if p.Pattern == "*.sfv" {
			sfvID = p.ID
		}
	}
	if sfvID == 0 {
		t.Fatal("*.sfv shipped default not found")
	}

	if err := cache.DeleteExclusionPattern(t.Context(), sfvID); err != nil {
		t.Fatalf("DeleteExclusionPattern: %v", err)
	}
	if _, ok := cache.Snapshot().ExclusionPattern(sfvID); ok {
		t.Error("builtin pattern still exists after Delete -- ADR-53 requires shipped defaults stay removable")
	}
}

func TestMatchExclusion_RefuseWinsOverIgnore(t *testing.T) {
	patterns := []settings.ExclusionPattern{
		{Pattern: "*.exe", Action: settings.ExclusionRefuse, Enabled: true},
		{Pattern: "release.exe", Action: settings.ExclusionIgnore, Enabled: true},
	}

	action, matched, ok := settings.MatchExclusion(patterns, "release.exe", "music", nil)
	if !ok || action != settings.ExclusionRefuse {
		t.Fatalf("action = %q ok=%v, want refuse -- it must win even when an ignore pattern also matches", action, ok)
	}
	if matched.Pattern != "*.exe" {
		t.Errorf("matched = %+v, want the refuse pattern", matched)
	}
}

func TestMatchExclusion_ScopedToMediaTypeAndCategory(t *testing.T) {
	musicOnly := int64(1)
	patterns := []settings.ExclusionPattern{
		{Pattern: "*.iso", Action: settings.ExclusionRefuse, MediaType: "music", Enabled: true},
		{Pattern: "*.log", Action: settings.ExclusionIgnore, CategoryID: &musicOnly, Enabled: true},
	}

	// *.iso must not fire for roms.
	if _, _, ok := settings.MatchExclusion(patterns, "game.iso", "rom", nil); ok {
		t.Error("*.iso (scoped to music) matched a rom file")
	}
	// *.iso must fire for music.
	if action, _, ok := settings.MatchExclusion(patterns, "bonus.iso", "music", nil); !ok || action != settings.ExclusionRefuse {
		t.Errorf("*.iso did not match a music file: action=%q ok=%v", action, ok)
	}
	// *.log is scoped to category 1 only.
	other := int64(2)
	if _, _, ok := settings.MatchExclusion(patterns, "session.log", "music", &other); ok {
		t.Error("*.log (scoped to category 1) matched a different category")
	}
	if action, _, ok := settings.MatchExclusion(patterns, "session.log", "music", &musicOnly); !ok || action != settings.ExclusionIgnore {
		t.Errorf("*.log did not match its own category: action=%q ok=%v", action, ok)
	}
}

func TestMatchExclusion_DisabledPatternNeverMatches(t *testing.T) {
	patterns := []settings.ExclusionPattern{
		{Pattern: "*.exe", Action: settings.ExclusionRefuse, Enabled: false},
	}
	if _, _, ok := settings.MatchExclusion(patterns, "virus.exe", "music", nil); ok {
		t.Error("a disabled pattern matched -- Enabled must be checked")
	}
}

func TestMatchExclusion_NoMatchReturnsFalse(t *testing.T) {
	patterns := []settings.ExclusionPattern{
		{Pattern: "*.exe", Action: settings.ExclusionRefuse, Enabled: true},
	}
	if _, _, ok := settings.MatchExclusion(patterns, "track.flac", "music", nil); ok {
		t.Error("an unrelated file matched")
	}
}
