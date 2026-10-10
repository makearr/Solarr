package library_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// One song on its album as a flac and on a compilation as a 96 kbit mp3 is one track twice, whatever the bytes are.
func TestDuplicates_FindsTheSameTrackInDifferentFormatsAndLeavesDifferentOnesAlone(t *testing.T) {
	opened, _ := newLibrary(t)
	type file struct {
		album, title, codec string
		duration            int64
		bitrate             int64
		path                string
	}
	files := []file{
		{"Album", "Song (Remastered 2011)", "flac", 215_000, 900_000, "Album/01.flac"},
		{"Compilation", "Song", "mp3", 216_500, 96_000, "Comp/07.mp3"},
		{"Album", "Other", "flac", 180_000, 900_000, "Album/02.flac"},
		{"Compilation", "Other", "mp3", 240_000, 96_000, "Comp/08.mp3"}, // a different length: another recording
		{"Compilation", "Song", "mp3", 0, 96_000, "Comp/09.mp3"},        // no measured length: cannot be told apart
		// Three takes of one length are three recordings: a bracketed mix or instrumental is not a release note.
		{"Single", "Lantern (12\" Version)", "flac", 380_000, 900_000, "Single/01.flac"},
		{"Single", "Lantern (Dub Mix)", "flac", 382_000, 900_000, "Single/03.flac"},
		{"Single", "Lantern [Instrumental]", "flac", 379_000, 900_000, "Single/04.flac"},
	}
	works := map[string]int64{}
	items := map[string]int64{}
	for _, f := range files {
		if _, ok := works[f.album]; !ok {
			works[f.album] = createWork(t, opened, "music", f.album).ID
			items[f.album] = createDesired(t, opened, works[f.album], "SET", 10).ID
		}
	}
	for index, f := range files {
		ref := fmt.Sprintf("1:%d", index+1)
		createPart(t, opened, works[f.album], ref, fmt.Sprintf("%03d", index), f.title)
		object := createObject(t, opened, fmt.Sprintf("h%d", index), 1000+int64(index))
		attributes := intern(t, opened, "attr-"+f.codec, "")
		err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := librarydb.New(tx).AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
				MediaObjectID: object.ID, RootFolderID: 1, RelPath: f.path, LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
			}); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE media_object SET duration_ms = ?, overall_bitrate = ?, measured_attributes_id = ? WHERE id = ?`,
				sql.NullInt64{Int64: f.duration, Valid: f.duration > 0}, f.bitrate, attributes.ID, object.ID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		attach(t, opened, items[f.album], object.ID, "primary", ref)
	}

	groups, err := library.NewDuplicates(opened).Groups(t.Context(), 1, []int64{1}, 50, true)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want one: Song as a flac and an mp3", groups)
	}
	var paths []string
	for _, copy := range groups[0].Copies {
		paths = append(paths, copy.Paths[0].RelPath)
	}
	if len(paths) != 2 || paths[0] != "Album/01.flac" || paths[1] != "Comp/07.mp3" {
		t.Errorf("copies = %v, want the flac and the 96k mp3 of Song", paths)
	}
}

// Two episodes that share a title apart from a bracketed number and run the same length are two episodes, not a
// duplicate; only two files on one episode record are.
func TestDuplicates_VideoIsToldApartByEpisodeRecordNotByTitle(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "tv", "Show").ID
	item := createDesired(t, opened, work, "SET", 10).ID
	type file struct{ ref, title, path string }
	files := []file{
		{"s24e01", "Auditions (1)", "Show/S24E01.mp4"},
		{"s24e04", "Auditions (4)", "Show/S24E04.mp4"},
		{"s24e05", "Finale", "Show/S24E05.mkv"},
		{"s24e05", "Finale", "Show/S24E05 (1).mkv"},
	}
	parts := map[string]bool{}
	for index, f := range files {
		if !parts[f.ref] {
			createPart(t, opened, work, f.ref, fmt.Sprintf("%03d", index), f.title)
			parts[f.ref] = true
		}
		object := createObject(t, opened, fmt.Sprintf("v%d", index), 1000+int64(index))
		err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := librarydb.New(tx).AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
				MediaObjectID: object.ID, RootFolderID: 1, RelPath: f.path, LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
			}); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE media_object SET duration_ms = 2700000 WHERE id = ?`, object.ID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		attach(t, opened, item, object.ID, "primary", f.ref)
	}
	groups, err := library.NewDuplicates(opened).Groups(t.Context(), 1, []int64{1}, 50, false)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Copies) != 2 || groups[0].Copies[0].Paths[0].RelPath[:10] != "Show/S24E0" {
		t.Fatalf("groups = %+v, want only the two files of S24E05", groups)
	}
	for _, copy := range groups[0].Copies {
		if copy.Paths[0].RelPath != "Show/S24E05.mkv" && copy.Paths[0].RelPath != "Show/S24E05 (1).mkv" {
			t.Errorf("unexpected copy %q", copy.Paths[0].RelPath)
		}
	}
}
