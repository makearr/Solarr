package music_test

import (
	"path"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

func newHousekeeping(t *testing.T, fileManagement settings.FileManagement) (music.Housekeeping, *fsx.Mem, library.People) {
	t.Helper()
	opened, fsys, _ := newCommitterEnv(t)
	desired := library.NewDesired(opened)
	free, less := int64(1<<30), int64(1<<20)
	roots := &fakePlacementRoots{
		roots: map[int64]settings.RootFolder{
			commitDestRootID:        {ID: commitDestRootID, Path: commitDestRoot, Online: true, Writable: true, FreeBytes: &less, TotalBytes: &free},
			commitDestRootEmptierID: {ID: commitDestRootEmptierID, Path: commitDestRootEmptier, Online: true, Writable: true, FreeBytes: &free, TotalBytes: &free},
		},
		categoryRoots: []settings.CategoryRoot{
			{CategoryID: commitCategoryID, RootFolderID: commitDestRootID, AcceptsNew: true},
			{CategoryID: commitCategoryID, RootFolderID: commitDestRootEmptierID, AcceptsNew: true},
		},
	}
	for _, root := range []string{commitDestRoot, commitDestRootEmptier} {
		if err := fsys.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	people := library.NewPeople(opened)
	housekeeping := music.NewHousekeeping(music.HousekeepingConfig{
		FS: fsys, Roots: roots, Affinity: newFakePlacementAffinity(), Desired: &desired,
		Categories:    fakeCategories{commitCategoryID: {ID: commitCategoryID, FileManagement: fileManagement}},
		Subscriptions: library.NewSubscriptions(opened), People: people,
	})
	return housekeeping, fsys, people
}

// Sonarr removes empty folders inside an artist, and the artist folder itself only when it would
// not create it again.
func TestHousekeeping_DeletesEmptyFolders(t *testing.T) {
	for _, createArtistFolders := range []bool{false, true} {
		fileManagement := settings.DefaultFileManagement
		fileManagement.DeleteEmptyFolders = true
		fileManagement.CreateEmptyArtistFolders = createArtistFolders
		housekeeping, fsys, _ := newHousekeeping(t, fileManagement)
		for _, folder := range []string{"Heka/Zanuvode (1988)", "Solo"} {
			if err := fsys.MkdirAll(path.Join(commitDestRoot, folder), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		err := housekeeping.AfterSweep(t.Context(), settings.RootFolder{ID: commitDestRootID, Path: commitDestRoot},
			commitCategoryID, scan.SweepOutcome{EmptyDirectories: []string{"Heka/Zanuvode (1988)", "Solo"}})
		if err != nil {
			t.Fatalf("AfterSweep: %v", err)
		}
		for folder, wantKept := range map[string]bool{
			"Heka/Zanuvode (1988)": false, "Heka": createArtistFolders, "Solo": createArtistFolders, "": true,
		} {
			_, err := fsys.Stat(path.Join(commitDestRoot, folder))
			if kept := err == nil; kept != wantKept {
				t.Errorf("create=%v: %q kept = %v, want %v", createArtistFolders, folder, kept, wantKept)
			}
		}
	}
}

// A monitored artist's folder is created once, on the root an import would choose.
func TestHousekeeping_CreatesAMonitoredArtistsFolderOnTheChosenRoot(t *testing.T) {
	fileManagement := settings.DefaultFileManagement
	fileManagement.CreateEmptyArtistFolders = true
	housekeeping, fsys, people := newHousekeeping(t, fileManagement)
	at := time.Unix(1_700_000_000, 0)
	personID, err := people.ResolvePerson(t.Context(), at, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "a1", Person: library.PersonIdentity{Name: "Heka", SortName: "Heka"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if _, err := people.Subscribe(t.Context(), at, personID, 1, library.SubscriptionRequest{
		MediaTypes: []string{"music"}, CategoryID: commitCategoryID,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	for _, root := range []settings.RootFolder{
		{ID: commitDestRootID, Path: commitDestRoot}, {ID: commitDestRootEmptierID, Path: commitDestRootEmptier},
	} {
		if err := housekeeping.AfterSweep(t.Context(), root, commitCategoryID, scan.SweepOutcome{}); err != nil {
			t.Fatalf("AfterSweep(%s): %v", root.Path, err)
		}
	}
	if _, err := fsys.Stat(path.Join(commitDestRootEmptier, "Heka")); err != nil {
		t.Errorf("Heka not created on the emptier root: %v", err)
	}
	if _, err := fsys.Stat(path.Join(commitDestRoot, "Heka")); err == nil {
		t.Error("Heka created on the fuller root too")
	}
}
