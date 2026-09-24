package downloadwatch_test

import (
	"context"
	"log/slog"
	"path"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/downloadwatch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

type folders []settings.DownloadFolder

func (f folders) DownloadFolders(context.Context) ([]settings.DownloadFolder, error) { return f, nil }

type sessions map[string]bool

func (s sessions) HasSessionFor(_ context.Context, source string) (bool, error) {
	return s[source], nil
}

type libraries struct{}

func (libraries) CategoryForMediaType(string) (settings.Category, bool) {
	return settings.Category{ID: 7}, true
}

func write(t *testing.T, fsys *fsx.Mem, file string) {
	t.Helper()
	if err := fsys.MkdirAll(path.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := fsys.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("audio"))
	_ = w.Close()
}

// Only a settled entry that was never imported is taken: a download still being written, one
// imported before, and a hidden temp file are all left alone.
func TestRun_ImportsEachSettledEntryOnce(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	fsys := fsx.NewMem(fake)
	write(t, fsys, "/downloads/Settled Album/01.flac")
	write(t, fsys, "/downloads/Imported Before/01.flac")
	write(t, fsys, "/other/Hinted Album/01.flac")
	fake.Advance(15 * time.Minute)
	write(t, fsys, "/downloads/Still Writing/01.flac")
	write(t, fsys, "/downloads/.incomplete/part.flac")
	write(t, fsys, "/unwatched/Album/01.flac")
	fake.Advance(time.Minute)

	hint := int64(3)
	var enqueued []importjob.Payload
	job := downloadwatch.NewJob(downloadwatch.Config{
		Folders: folders{
			{Path: "/downloads", Watch: true, Readable: true},
			{Path: "/other", Watch: true, Readable: true, CategoryID: &hint, TransferLadder: []string{"copy"}},
			{Path: "/unwatched", Watch: false, Readable: true},
		},
		FS: fsys, Sessions: sessions{"/downloads/Imported Before": true}, Libraries: libraries{},
		Enqueue: func(_ context.Context, payload importjob.Payload) error {
			enqueued = append(enqueued, payload)
			return nil
		},
		Clock: fake, Settle: downloadwatch.DefaultSettle, Logger: slog.New(slog.DiscardHandler),
	})
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}

	sort.Slice(enqueued, func(i, j int) bool { return enqueued[i].SourcePath < enqueued[j].SourcePath })
	anyLibrary, hinted := int64(7), hint
	want := []importjob.Payload{
		{SourcePath: "/downloads/Settled Album", Origin: "watched", CategoryID: &anyLibrary},
		{SourcePath: "/other/Hinted Album", Origin: "watched", CategoryID: &hinted, TransferLadder: []string{"copy"}},
	}
	if !reflect.DeepEqual(enqueued, want) {
		t.Errorf("enqueued %+v\nwant     %+v", enqueued, want)
	}
}
