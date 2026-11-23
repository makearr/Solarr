package music

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/lrclib"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/sidecar"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// LyricsJobType writes the lyrics LRCLIB holds beside the tracks of the libraries that asked for them.
const LyricsJobType = jobs.Type("music.lyrics")

// LyricsDefinition runs in the background; one file is one request, paced by LRCLIB's bucket.
var LyricsDefinition = jobs.Definition{
	Name: "Writing lyrics beside tracks",
	Type: LyricsJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 1,
}

const (
	// LyricsOff writes nothing; LyricsOn is the value of the library's lyrics setting that switches the pass on.
	LyricsOff = "off"
	// LyricsOn asks LRCLIB for the library's tracks.
	LyricsOn = "on"

	lyricsPage   = 100
	lyricsPerRun = 500
	// lyricsRetryAfter is how long a track LRCLIB had nothing for waits before it is asked again.
	lyricsRetryAfter = 30 * 24 * time.Hour
)

// LyricsRoot is one root folder the pass may write on: online, writable, not read only, in a library that switched lyrics on.
type LyricsRoot struct {
	ID   int64
	Path string
}

// LyricsSource answers the lyrics of a recording.
type LyricsSource interface {
	Get(ctx context.Context, track lrclib.Track) (lrclib.Lyrics, bool, error)
}

// LyricsJob asks LRCLIB for tracks without a lyrics file and writes what it holds, never over a file a person put there.
type LyricsJob struct {
	Files  library.Lyrics
	Roots  func(ctx context.Context) ([]LyricsRoot, error)
	Source LyricsSource
	FS     fsx.FS
	Clock  clock.Clock
	Logger *slog.Logger
}

// Run asks about up to lyricsPerRun tracks; the next run carries on.
func (j LyricsJob) Run(ctx context.Context, _ jobs.Job) error {
	roots, err := j.Roots(ctx)
	if err != nil || len(roots) == 0 {
		return err
	}
	byID := make(map[int64]LyricsRoot, len(roots))
	ids := make([]int64, len(roots))
	for i, root := range roots {
		byID[root.ID], ids[i] = root, root.ID
	}
	asked, written := 0, 0
	retryBefore := j.Clock.Now().Add(-lyricsRetryAfter).Unix()
	for after := int64(0); asked < lyricsPerRun; {
		candidates, err := j.Files.Candidates(ctx, ids, after, retryBefore, lyricsPage)
		if err != nil || len(candidates) == 0 {
			return err
		}
		for _, candidate := range candidates {
			after = candidate.MediaObjectID
			made, err := j.one(ctx, byID[candidate.RootFolderID], candidate)
			if err != nil {
				return fmt.Errorf("lyrics for file %d: %w", candidate.MediaObjectID, err)
			}
			asked++
			written += made
		}
	}
	j.Logger.InfoContext(ctx, "lyrics asked", slog.Int("asked", asked), slog.Int("written", written))
	return nil
}

// one looks the track up unless a lyrics file is already there, writes the file and records the attempt; a track without
// a title is skipped without a record, so naming it later asks again.
func (j LyricsJob) one(ctx context.Context, root LyricsRoot, candidate library.LyricsCandidate) (int, error) {
	if candidate.TrackTitle == "" || candidate.ArtistName == "" {
		return 0, nil
	}
	file, err := mover.RootRelativePath(j.FS, root.Path, candidate.RelPath)
	if err != nil {
		return 0, nil //nolint:nilerr // a path outside the root is not this pass's to write
	}
	stem := strings.TrimSuffix(file, path.Ext(file))
	if j.lyricsFileExists(stem) {
		return 0, j.Files.Record(ctx, candidate.MediaObjectID, j.Clock.Now().Unix(), true)
	}
	lyrics, found, err := j.Source.Get(ctx, lrclib.Track{
		Artist: candidate.ArtistName, Title: candidate.TrackTitle, Album: candidate.AlbumTitle, DurationSeconds: candidate.DurationMs / 1000,
	})
	if err != nil {
		j.Logger.WarnContext(ctx, "lyrics lookup failed", slog.Int64("media_object_id", candidate.MediaObjectID), slog.Any("error", err))
		return 0, ctx.Err()
	}
	made := 0
	if found {
		extension, text := ".txt", lyrics.Plain
		if lyrics.Synced != "" {
			extension, text = ".lrc", lyrics.Synced
		}
		made, err = sidecar.Writer{FS: j.FS}.Once(stem+extension, func() ([]byte, error) { return []byte(text + "\n"), nil })
		if err != nil {
			return 0, err
		}
	}
	return made, j.Files.Record(ctx, candidate.MediaObjectID, j.Clock.Now().Unix(), found)
}

func (j LyricsJob) lyricsFileExists(stem string) bool {
	for _, extension := range []string{".lrc", ".txt"} {
		if _, err := j.FS.Lstat(stem + extension); err == nil {
			return true
		}
	}
	return false
}

// EnqueueLyrics submits one run; a second while one waits is the same work.
func EnqueueLyrics(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: LyricsJobType, ActionKey: string(LyricsJobType), Priority: jobs.PriorityLow})
}
