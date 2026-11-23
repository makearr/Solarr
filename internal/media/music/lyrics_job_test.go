package music_test

import (
	"context"
	"database/sql"
	"log/slog"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/lrclib"
)

type fakeLyrics struct {
	asked  []lrclib.Track
	answer map[string]lrclib.Lyrics
}

func (f *fakeLyrics) Get(_ context.Context, track lrclib.Track) (lrclib.Lyrics, bool, error) {
	f.asked = append(f.asked, track)
	lyrics, found := f.answer[track.Title]
	return lyrics, found, nil
}

// nameTrack gives the file's work an artist and a track title, as an identified album import does.
func nameTrack(t *testing.T, opened *store.Store, workID int64, artist, title string) {
	t.Helper()
	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		for _, statement := range []string{
			`INSERT INTO person (id, name, sort_name, added_at) VALUES (900, '` + artist + `', '` + artist + `', 1)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE work SET lead_person_id = 900 WHERE id = ?`, workID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_part (id, work_id, media_ref, title) VALUES (900, ?, 'disc1:track1', ?)`, workID, title); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO desired_item (id, work_id, media_ref, category_id, profile_id, state)
			VALUES (900, ?, 'disc1:track1', 1, 1, 'COMPLETE')`, workID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO desired_item_file (desired_item_id, media_object_id, media_ref, added_at)
			SELECT 900, media_object_id, 'disc1:track1', 1 FROM work_file WHERE work_id = ?`, workID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLyrics_WritesSyncedBesideTheTrackOnceAndNeverOverAPersonsFile(t *testing.T) {
	opened, fs, fake := newTestLibrary(t)
	workID := identifyFile(t, opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	nameTrack(t, opened, workID, "Heka", "Opening")
	if err := fs.MkdirAll("/music/Heka/Zanuvode", 0o755); err != nil {
		t.Fatal(err)
	}
	source := &fakeLyrics{answer: map[string]lrclib.Lyrics{"Opening": {Plain: "words", Synced: "[00:01.00] words"}}}
	roots := []music.LyricsRoot{{ID: 1, Path: "/music"}}
	job := music.LyricsJob{
		Files: library.NewLyrics(opened), Roots: func(context.Context) ([]music.LyricsRoot, error) { return roots, nil },
		Source: source, FS: fs, Clock: fake, Logger: slog.New(slog.DiscardHandler),
	}
	for range 2 {
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFile(t, fs, "/music/Heka/Zanuvode/01.lrc"); got != "[00:01.00] words\n" {
		t.Errorf("lrc = %q", got)
	}
	want := []lrclib.Track{{Artist: "Heka", Title: "Opening", Album: "Heka/Zanuvode/01.m4a"}}
	if !reflect.DeepEqual(source.asked, want) {
		t.Errorf("asked = %+v, want %+v (once, the second run has nothing to ask)", source.asked, want)
	}
}

func TestLyrics_APersonsOwnFileIsKeptAndNothingIsWrittenWithoutARoot(t *testing.T) {
	opened, fs, fake := newTestLibrary(t)
	workID := identifyFile(t, opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	nameTrack(t, opened, workID, "Heka", "Opening")
	putFile(t, fs, "/music/Heka/Zanuvode/01.txt", "mine")
	source := &fakeLyrics{answer: map[string]lrclib.Lyrics{"Opening": {Plain: "words"}}}
	var roots []music.LyricsRoot
	job := music.LyricsJob{
		Files: library.NewLyrics(opened), Roots: func(context.Context) ([]music.LyricsRoot, error) { return roots, nil },
		Source: source, FS: fs, Clock: fake, Logger: slog.New(slog.DiscardHandler),
	}
	if err := job.Run(t.Context(), jobs.Job{}); err != nil || len(source.asked) != 0 {
		t.Fatalf("no root: err %v, asked %v", err, source.asked)
	}
	roots = []music.LyricsRoot{{ID: 1, Path: "/music"}}
	if err := job.Run(t.Context(), jobs.Job{}); err != nil || len(source.asked) != 0 {
		t.Fatalf("own file: err %v, asked %v", err, source.asked)
	}
	if got := readFile(t, fs, "/music/Heka/Zanuvode/01.txt"); got != "mine" {
		t.Errorf("txt = %q", got)
	}
}
