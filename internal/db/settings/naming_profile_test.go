package settings_test

import "testing"

// TestNamingProfile_ShippedDefaultIsSeeded is todo 4.5's own remaining
// schema step (migration 0013): naming_profile exists so a second preset is
// a data-only change later, seeded today with exactly the one template
// music.DefaultFolderTemplate/DefaultFileTemplate already renders in
// production -- this table has no code reader yet (naming_profile.sql's own
// doc comment), so it is exercised directly the way discovery_job's own
// tests bypass the Cache wrapper for the same reason (newSettingsStore).
func TestNamingProfile_ShippedDefaultIsSeeded(t *testing.T) {
	_, queries := newSettingsStore(t)

	profiles, err := queries.ListNamingProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListNamingProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("len(profiles) = %d, want 1 shipped default", len(profiles))
	}

	profile := profiles[0]
	if profile.Name != "Default (music)" {
		t.Errorf("Name = %q, want %q", profile.Name, "Default (music)")
	}
	const wantFolder = "{Artist Name}/{Album Title} ({Release Year})"
	const wantFile = "{Artist Name} - {track:00} - {Track Title}"
	if profile.FolderTemplate != wantFolder {
		t.Errorf("FolderTemplate = %q, want %q (music.DefaultFolderTemplate)", profile.FolderTemplate, wantFolder)
	}
	if profile.FileTemplate != wantFile {
		t.Errorf("FileTemplate = %q, want %q (music.DefaultFileTemplate)", profile.FileTemplate, wantFile)
	}
}
