package music

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/jobs"
)

// CutoffJobType is Lidarr's Cutoff Unmet, computed: every album with all its tracks is judged against
// its library's cutoff and marked UPGRADABLE when a file falls short (M1d).
const CutoffJobType = jobs.Type("music.cutoff_check")

// CutoffDefinition runs one check at a time; the next scheduled run is its retry.
var CutoffDefinition = jobs.Definition{
	Name: "Checking albums against the quality cutoff", Type: CutoffJobType, Pool: jobs.PoolMaintenance,
	Mutating: true, MaxAttempts: 1,
}

const cutoffPageSize = 200

// CutoffJob marks albums UPGRADABLE or COMPLETE by what the probe measured of their files.
type CutoffJob struct {
	Desired  library.Desired
	Settings *settings.Cache
	Logger   *slog.Logger
}

// Run walks every monitored complete album once. An album with a file not yet measured is left as it is.
func (j CutoffJob) Run(ctx context.Context, job jobs.Job) error {
	snapshot := j.Settings.Snapshot()
	var afterID int64
	var checked, upgradable, changed int
	for {
		items, err := j.Desired.CutoffPage(ctx, afterID, cutoffPageSize)
		if err != nil {
			return err
		}
		for _, item := range items {
			afterID = item.ID
			if err := ctx.Err(); err != nil {
				return err
			}
			state, decided, err := j.stateFor(ctx, snapshot, item)
			if err != nil {
				return err
			}
			job.Progress.Add(1)
			if !decided {
				continue
			}
			checked++
			if state == string(ItemStateUpgradable) {
				upgradable++
			}
			if state != item.State {
				if err := j.Desired.SetAlbumState(ctx, item.ID, state, item.PresentCount); err != nil {
					return err
				}
				changed++
			}
		}
		if len(items) < cutoffPageSize {
			break
		}
	}
	j.logger().Info("cutoff check finished", slog.Int("albums", checked), slog.Int("cutoff_unmet", upgradable),
		slog.Int("changed", changed))
	return nil
}

// ItemStateUpgradable and ItemStateComplete are the two states the cutoff check moves an album between.
const (
	ItemStateUpgradable = "UPGRADABLE"
	ItemStateComplete   = "COMPLETE"
)

// stateFor judges every file of one album; decided is false while any file is unmeasured.
func (j CutoffJob) stateFor(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (string, bool, error) {
	category, found := snapshot.Category(item.CategoryID)
	if !found {
		return "", false, nil
	}
	files, err := j.Desired.MeasuredFiles(ctx, item.WorkID)
	if err != nil {
		return "", false, fmt.Errorf("music: cutoff check item %d: %w", item.ID, err)
	}
	if len(files) == 0 {
		return "", false, nil
	}
	for _, file := range files {
		if !file.Measured {
			return "", false, nil
		}
	}
	for _, file := range files {
		judgement, judged := quality.Judge(snapshot, category, measuredFacts(file))
		if !judged {
			return ItemStateComplete, true, nil
		}
		if !judgement.MeetsCutoff {
			return ItemStateUpgradable, true, nil
		}
	}
	return ItemStateComplete, true, nil
}

// measuredFacts is a measured file as the facts a profile judges; the file's own type, not ffprobe's
// demuxer list, is its container.
func measuredFacts(file library.MeasuredFile) score.Facts {
	return score.Facts{File: score.FileFacts{
		AudioCodec: file.AudioCodec, Container: file.Extension, Lossless: file.Lossless,
		AudioBitrate: file.BitrateKbps * 1000, OverallBitrate: file.BitrateKbps * 1000,
	}}
}

func (j CutoffJob) logger() *slog.Logger {
	if j.Logger == nil {
		return slog.Default()
	}
	return j.Logger
}
