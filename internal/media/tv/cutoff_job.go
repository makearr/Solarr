package tv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/jobs"
)

// CutoffJobType is Sonarr's Cutoff Unmet, computed: every monitored season is judged against its library's target or
// cutoff and marked UPGRADABLE when an episode file falls short.
const CutoffJobType = jobs.Type("tv.cutoff_check")

// CutoffDefinition runs one check at a time; the next scheduled run is its retry.
var CutoffDefinition = jobs.Definition{
	Name: "Checking seasons against the quality cutoff", Type: CutoffJobType, Pool: jobs.PoolMaintenance,
	Mutating: true, MaxAttempts: 1,
}

const cutoffPageSize = 200

// Item states the check moves a season between.
const (
	ItemStateUpgradable = "UPGRADABLE"
	ItemStateComplete   = "COMPLETE"
)

// CutoffPayload limits a run to one series, the one a download was just imported into; empty is every series.
type CutoffPayload struct {
	WorkID int64 `json:"work_id,omitempty"`
}

// CutoffJob settles an episode a download brought a second file for (the strictly better file stays, a tie keeps the
// owned one), then marks each season UPGRADABLE or COMPLETE by what the probe measured.
type CutoffJob struct {
	Desired  library.Desired
	Series   library.SeriesReader
	Settings *settings.Cache
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded, false for a
	// download that measured no better. Nil leaves every file in place.
	Recycle func(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error
	Logger  *slog.Logger
}

// Run walks every monitored season once; a season with a file not yet measured keeps its state.
func (j CutoffJob) Run(ctx context.Context, job jobs.Job) error {
	var payload CutoffPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Terminalf("cutoff.payload", string(CutoffJobType), err)
		}
	}
	snapshot := j.Settings.Snapshot()
	var afterID int64
	var checked, upgradable, changed, replaced int
	for {
		items, err := j.Desired.TVCutoffPage(ctx, afterID, cutoffPageSize)
		if err != nil {
			return err
		}
		for _, item := range items {
			afterID = item.ID
			if err := ctx.Err(); err != nil {
				return err
			}
			if payload.WorkID != 0 && item.WorkID != payload.WorkID {
				continue
			}
			settled, err := j.settle(ctx, snapshot, item)
			if err != nil {
				return err
			}
			replaced += settled
			if item.State == "PARTIAL" {
				continue // a season with gaps is not marked; only a second file in it is settled
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
			if state == ItemStateUpgradable {
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
	j.logger().Info("tv cutoff check finished", slog.Int("seasons", checked), slog.Int("cutoff_unmet", upgradable),
		slog.Int("changed", changed), slog.Int("files_replaced", replaced))
	return nil
}

// settle keeps, per episode a download brought a second file for, the strictly better measured file and recycles the
// rest; answers how many were recycled.
func (j CutoffJob) settle(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (int, error) {
	if j.Recycle == nil {
		return 0, nil
	}
	files, err := j.Series.SeasonFiles(ctx, item.ID)
	if err != nil {
		return 0, err
	}
	byEpisode := map[string][]library.SeasonFile{}
	for _, file := range files {
		byEpisode[file.Ref] = append(byEpisode[file.Ref], file)
	}
	var contested bool
	for _, episode := range byEpisode {
		contested = contested || len(episode) > 1
	}
	if !contested {
		return 0, nil
	}
	downloaded, err := j.Desired.DownloadedFiles(ctx, item.WorkID)
	if err != nil {
		return 0, err
	}
	rules, err := judge.LibraryProfiles(ctx, j.Desired, snapshot, item.ID, item.CategoryID, item.ProfileID)
	if errors.Is(err, judge.ErrNoQualityProfile) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	language := judge.LanguagePolicyFor(snapshot, item.CategoryID)
	recycled := 0
	for _, episode := range byEpisode {
		if len(episode) < 2 || !anyBrought(episode, downloaded) || !allMeasured(episode) {
			continue
		}
		keeper := episode[0]
		for _, file := range episode {
			if !downloaded[file.File.MediaObjectID] {
				keeper = file
				break
			}
		}
		for _, file := range episode {
			if judge.StrictlyBetter(judge.EvaluateInOrder(rules, FileFacts(file.File), language), judge.EvaluateInOrder(rules, FileFacts(keeper.File), language)) {
				keeper = file
			}
		}
		for _, file := range episode {
			if file.File.MediaObjectID == keeper.File.MediaObjectID {
				continue
			}
			if err := j.Recycle(ctx, file.File.MediaObjectID, item.CategoryID, !downloaded[file.File.MediaObjectID]); err != nil {
				return recycled, err
			}
			recycled++
		}
	}
	return recycled, nil
}

func anyBrought(files []library.SeasonFile, downloaded map[int64]bool) bool {
	for _, file := range files {
		if downloaded[file.File.MediaObjectID] {
			return true
		}
	}
	return false
}

func allMeasured(files []library.SeasonFile) bool {
	for _, file := range files {
		if !file.Measured {
			return false
		}
	}
	return true
}

// stateFor judges every episode file of one season; decided is false while any is unmeasured or there is none.
func (j CutoffJob) stateFor(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (string, bool, error) {
	category, found := snapshot.Category(item.CategoryID)
	if !found {
		return "", false, nil
	}
	if !category.QualityUpgrades {
		return ItemStateComplete, true, nil
	}
	files, err := j.Series.SeasonFiles(ctx, item.ID)
	if err != nil {
		return "", false, fmt.Errorf("tv: cutoff check item %d: %w", item.ID, err)
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
		if !file.Monitored {
			continue
		}
		judgement, judged := quality.Judge(snapshot, category, FileFacts(file.File))
		if !judged {
			return ItemStateComplete, true, nil
		}
		if !judgement.MeetsCutoff {
			return ItemStateUpgradable, true, nil
		}
	}
	return ItemStateComplete, true, nil
}

func (j CutoffJob) logger() *slog.Logger {
	if j.Logger == nil {
		return slog.Default()
	}
	return j.Logger
}

// FileFacts is what the probe measured of an episode's file, as the facts a profile or a target judges.
func FileFacts(file library.EpisodeFile) score.Facts {
	return score.Facts{File: score.FileFacts{
		Resolution: file.Resolution, VideoCodec: file.VideoCodec, HDR: file.HDR, AudioCodec: file.AudioCodec,
		AudioChannels: file.AudioChannels, Container: file.Container, AudioLanguages: file.Languages, SubtitleLanguages: file.Subtitles,
		FrameRate: file.FrameRate, VideoBitrate: file.VideoBitrate, AudioBitrate: file.AudioBitrate,
	}}
}
