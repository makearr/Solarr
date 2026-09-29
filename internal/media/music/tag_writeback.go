package music

import (
	"context"
	"log/slog"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// TagWritebackJobType is the sweep that writes MusicBrainz ids into identified files (revision D3).
const TagWritebackJobType = jobs.Type("music.tag_writeback")

// TagWritebackActionKey deduplicates the sweep: one queued run covers every root.
const TagWritebackActionKey = "music.tag_writeback"

// TagWritebackDefinition runs in maintenance: it rewrites files, and nothing waits on it.
var TagWritebackDefinition = jobs.Definition{
	Type: TagWritebackJobType, Pool: jobs.PoolMaintenance, Mutating: true, MaxAttempts: 1,
}

// tagWritebackPage is one read of candidates and one helper batch; tagWritebackPerRun paces a
// sweep over a large library across runs, as the artwork sweep is paced.
const (
	tagWritebackPage   = 200
	tagWritebackPerRun = 2000
)

// The keys written, in Picard's names; mediatool's helper maps each per format.
const (
	tagReleaseGroupID = "musicbrainz_releasegroupid"
	tagReleaseID      = "musicbrainz_albumid"
	tagRecordingID    = "musicbrainz_trackid"
	tagAlbumArtistID  = "musicbrainz_albumartistid"
)

// TagRoot is one root folder the sweep may write under: online, writable, not read-only, in a
// music library whose "write ids into tags" is on.
type TagRoot struct {
	ID   int64
	Path string
}

// TagWritebackConfig is what the job needs; every field is required.
type TagWritebackConfig struct {
	Files  library.TagWriteback
	Roots  func(ctx context.Context) ([]TagRoot, error)
	Tool   mediatool.Tool
	Clock  clock.Clock
	Logger *slog.Logger
}

// TagWritebackJob fills missing ids into files' tags, never overwriting one (section 19.5).
type TagWritebackJob struct{ config TagWritebackConfig }

// NewTagWritebackJob builds the handler.
func NewTagWritebackJob(config TagWritebackConfig) *TagWritebackJob {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &TagWritebackJob{config: config}
}

var tagProviders = library.TagProviders{
	Work: ProviderReleaseGroup, Edition: ProviderRelease, Part: ProviderRecording, Person: ProviderArtist,
}

// Run writes up to tagWritebackPerRun files; the next scheduled run carries on.
func (j *TagWritebackJob) Run(ctx context.Context, _ jobs.Job) error {
	roots, err := j.config.Roots(ctx)
	if err != nil || len(roots) == 0 {
		return err
	}
	byID := make(map[int64]string, len(roots))
	ids := make([]int64, 0, len(roots))
	for _, root := range roots {
		byID[root.ID] = root.Path
		ids = append(ids, root.ID)
	}

	var afterID int64
	written, complete, failed := 0, 0, 0
	for done := 0; done < tagWritebackPerRun; {
		candidates, err := j.config.Files.Candidates(ctx, tagProviders, ids, afterID, tagWritebackPage)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			break
		}
		w, c, f, err := j.writePage(ctx, byID, candidates)
		if err != nil {
			return err
		}
		written, complete, failed = written+w, complete+c, failed+f
		done += len(candidates)
		afterID = candidates[len(candidates)-1].MediaObjectID
	}
	if written+complete+failed > 0 {
		j.config.Logger.Info("tag write-back finished",
			slog.Int("written", written), slog.Int("already_complete", complete), slog.Int("failed", failed))
	}
	return nil
}

// writePage reads one page's tags, writes what each file lacks, and records every file.
func (j *TagWritebackJob) writePage(
	ctx context.Context, roots map[int64]string, candidates []library.TagCandidate,
) (written, complete, failed int, err error) {
	paths := make([]string, len(candidates))
	for i, candidate := range candidates {
		paths[i] = path.Join(roots[candidate.RootFolderID], candidate.RelPath)
	}
	probes, err := j.config.Tool.ReadTags(ctx, paths)
	if err != nil {
		return 0, 0, 0, err
	}
	now := j.config.Clock.Now().Unix()

	var edits []mediatool.TagEdit
	var editOf []int
	for i, candidate := range candidates {
		if probes[i].Err != nil {
			failed++
			if err := j.config.Files.Record(ctx, candidate.MediaObjectID, now, "", probes[i].Err.Error()); err != nil {
				return 0, 0, 0, err
			}
			continue
		}
		missing := missingIDs(probes[i].Tags, candidate)
		if len(missing) == 0 {
			complete++
			if err := j.config.Files.Record(ctx, candidate.MediaObjectID, now, "", ""); err != nil {
				return 0, 0, 0, err
			}
			continue
		}
		edits = append(edits, mediatool.TagEdit{Path: paths[i], Set: missing})
		editOf = append(editOf, i)
	}
	if len(edits) == 0 {
		return 0, complete, failed, nil
	}

	failures, err := j.config.Tool.WriteTags(ctx, edits)
	if err != nil {
		return 0, 0, 0, err
	}
	for k, edit := range edits {
		candidate := candidates[editOf[k]]
		failure := ""
		if failures[k] != nil {
			failure = failures[k].Error()
			failed++
		} else {
			written++
		}
		if err := j.config.Files.Record(ctx, candidate.MediaObjectID, now, keysOf(edit.Set), failure); err != nil {
			return 0, 0, 0, err
		}
	}
	return written, complete, failed, nil
}

// missingIDs is what the file lacks of what is known: fill-only, a present value is never replaced.
func missingIDs(present mediatool.Tags, candidate library.TagCandidate) mediatool.Tags {
	missing := mediatool.Tags{}
	for key, value := range map[string]string{
		tagReleaseGroupID: candidate.WorkExternalID, tagReleaseID: candidate.EditionExternalID,
		tagRecordingID: candidate.PartExternalID, tagAlbumArtistID: candidate.PersonExternalID,
	} {
		if value != "" && present.First(key) == "" {
			missing[key] = value
		}
	}
	return missing
}

func keysOf(tags mediatool.Tags) string {
	keys := make([]string, 0, len(tags))
	for _, key := range []string{tagReleaseGroupID, tagReleaseID, tagRecordingID, tagAlbumArtistID} {
		if _, ok := tags[key]; ok {
			keys = append(keys, key)
		}
	}
	return strings.Join(keys, ",")
}

// EnqueueTagWriteback submits one sweep, deduplicated by TagWritebackActionKey.
func EnqueueTagWriteback(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: TagWritebackJobType, ActionKey: TagWritebackActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
}
