package music_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/moving/renamejob"
)

type fakeRenameWorks map[int64]library.WorkDetail

func (f fakeRenameWorks) Detail(_ context.Context, workID int64) (library.WorkDetail, error) {
	return f[workID], nil
}

func renameWorkDetailFixture(title string) library.WorkDetail {
	return library.WorkDetail{
		Work: librarydb.Work{ID: 1, CanonicalTitle: title, Year: sql.NullInt64{Int64: 1988, Valid: true}},
		Parts: []library.PartDetail{
			{Part: librarydb.WorkPart{ID: 100, WorkID: 1, MediaRef: "disc1:track1", Title: sql.NullString{String: "Vikuzo", Valid: true}}},
			{Part: librarydb.WorkPart{ID: 101, WorkID: 1, MediaRef: "disc1:track2", Title: sql.NullString{String: "Pemaru Tide", Valid: true}}},
		},
		Credits: []library.Credit{{PersonName: "Lova Kentu", Role: music.CreditRoleArtist}},
	}
}

func renameFile(id int64, rel, ref string) renamejob.File {
	return renamejob.File{FileOnRoot: library.FileOnRoot{MediaObjectID: id, RelPath: rel, SizeBytes: 100}, WorkID: 1, MediaRefs: []string{ref}}
}

// Music names each of an album's files by the track it holds; a file whose track the album lacks is left out.
func TestNameFiles_NamesEachTrackOfTheAlbum(t *testing.T) {
	cfg := music.RenameConfig{Works: fakeRenameWorks{1: renameWorkDetailFixture("Kozumi")}}
	named, err := cfg.NameFiles(t.Context(), settings.Category{ID: 1, MediaType: "music"}, 1, []renamejob.File{
		renameFile(1, "Old/01.flac", "disc1:track1"),
		renameFile(2, "Lova Kentu/Kozumi (1988)/Lova Kentu - 02 - Pemaru Tide.flac", "disc1:track2"),
		renameFile(3, "Old/99.flac", "disc1:track9"),
	})
	if err != nil {
		t.Fatalf("NameFiles: %v", err)
	}
	want := []renamejob.Named{
		{MediaObjectID: 1, RelPath: "Lova Kentu/Kozumi (1988)/Lova Kentu - 01 - Vikuzo.flac"},
		{MediaObjectID: 2, RelPath: "Lova Kentu/Kozumi (1988)/Lova Kentu - 02 - Pemaru Tide.flac"},
	}
	if !reflect.DeepEqual(named, want) {
		t.Errorf("named = %+v\nwant %+v", named, want)
	}
}

// Naming's path-length notice reaches the rename preview with the file it is about.
func TestNameFiles_CarriesNamingWarnings(t *testing.T) {
	cfg := music.RenameConfig{Works: fakeRenameWorks{1: renameWorkDetailFixture(strings.Repeat("Long Album Title ", 14))}}
	named, err := cfg.NameFiles(t.Context(), settings.Category{ID: 1, MediaType: "music"}, 1, []renamejob.File{renameFile(1, "Old/01.flac", "disc1:track1")})
	if err != nil {
		t.Fatalf("NameFiles: %v", err)
	}
	if len(named) != 1 || len(named[0].Warnings) == 0 {
		t.Errorf("named = %+v, want the long path's warning", named)
	}
}
