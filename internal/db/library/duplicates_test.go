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

	groups, err := library.NewDuplicates(opened).Groups(t.Context(), 1, []int64{1}, 50)
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
