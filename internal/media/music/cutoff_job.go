package music

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// CutoffJob marks albums UPGRADABLE or COMPLETE by what the probe measured of their files, first
// settling a track a download brought a second file for: the better file stays (M1d's replacement).
type CutoffJob struct {
	Desired  library.Desired
	Settings *settings.Cache
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded,
	// false for a download that measured no better. Nil leaves every file in place.
	Recycle func(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error
	Clock   clock.Clock
	Logger  *slog.Logger
}

// CheckCutoff implements cutoff.Check: an album with all its tracks is settled and judged; one with gaps is left.
func (j CutoffJob) CheckCutoff(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (cutoff.Outcome, error) {
	if item.State != ItemStateComplete && item.State != ItemStateUpgradable {
		return cutoff.Outcome{}, nil
	}
	settled, err := j.replaceSuperseded(ctx, snapshot, item)
	if err != nil {
		return cutoff.Outcome{}, err
	}
	state, decided, err := j.stateFor(ctx, snapshot, item)
	if err != nil {
		return cutoff.Outcome{}, err
	}
	// A recycle recomputed the item, so the judgment is written whatever the state read before.
	return cutoff.Outcome{Replaced: settled, State: state, Decided: decided, Rewrite: settled > 0}, nil
}

// CutoffPage implements cutoff.Check: a page of albums to check, or the one album workID names.
func (j CutoffJob) CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error) {
	if workID == 0 {
		return j.Desired.CutoffPage(ctx, afterID, limit)
	}
	items, err := j.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil {
		return nil, err
	}
	var page []library.CutoffItem
	for _, item := range items {
		if item.Monitored == 1 && (item.State == ItemStateComplete || item.State == ItemStateUpgradable) {
			page = append(page, library.CutoffItem{
				ID: item.ID, WorkID: item.WorkID, CategoryID: item.CategoryID, ProfileID: item.ProfileID,
				State: item.State, PresentCount: item.PresentCount,
			})
		}
	}
	return page, nil
}

// replaceSuperseded keeps, per track a download brought a second file for, the strictly better measured
// file (a tie keeps the owned one, Invariant 72) and recycles the rest; answers how many were recycled.
func (j CutoffJob) replaceSuperseded(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (int, error) {
	if j.Recycle == nil {
		return 0, nil
	}
	downloaded, err := j.Desired.DownloadedFiles(ctx, item.WorkID)
	if err != nil || len(downloaded) == 0 {
		return 0, err
	}
	if err := j.alignDownloaded(ctx, item, downloaded); err != nil {
		return 0, err
	}
	files, err := j.Desired.MeasuredFiles(ctx, item.WorkID)
	if err != nil {
		return 0, err
	}
	rules, err := judge.LibraryProfiles(ctx, j.Desired, snapshot, item.ID, item.CategoryID, item.ProfileID)
	if errors.Is(err, ErrNoQualityProfile) {
		return 0, nil // a library without a usable profile has nothing to judge by
	}
	if err != nil {
		return 0, err
	}
	language := judge.LanguagePolicyFor(snapshot, item.CategoryID)
	byTrack := map[string][]library.MeasuredFile{}
	for _, file := range files {
		byTrack[file.MediaRef] = append(byTrack[file.MediaRef], file)
	}
	recycled := 0
	for _, track := range byTrack {
		if len(track) < 2 || !anyDownloaded(track, downloaded) || !allMeasured(track) {
			continue
		}
		keeper := track[0]
		for _, file := range track {
			if !downloaded[file.MediaObjectID] {
				keeper = file // the owned file is the incumbent
				break
			}
		}
		for _, file := range track {
			if judge.StrictlyBetter(judge.EvaluateInOrder(rules, measuredFacts(file), language),
				judge.EvaluateInOrder(rules, measuredFacts(keeper), language)) {
				keeper = file
			}
		}
		for _, file := range track {
			if file.MediaObjectID == keeper.MediaObjectID {
				continue
			}
			if err := j.Recycle(ctx, file.MediaObjectID, item.CategoryID, !downloaded[file.MediaObjectID]); err != nil {
				return recycled, err
			}
			recycled++
		}
	}
	return recycled, nil
}

// alignDownloaded gives a download's files their tracks: import only aligns onto tracks with no file
// yet, so a replacement for an owned track was left without one.
func (j CutoffJob) alignDownloaded(ctx context.Context, item library.CutoffItem, downloaded map[int64]bool) error {
	attached, err := j.Desired.MeasuredFiles(ctx, item.WorkID)
	if err != nil {
		return err
	}
	onTrack := map[int64]bool{}
	for _, file := range attached {
		onTrack[file.MediaObjectID] = true
	}
	claims, err := j.Desired.TrackClaimsForWork(ctx, item.WorkID)
	if err != nil {
		return err
	}
	var candidates []TrackFile
	for _, claim := range claims {
		if downloaded[claim.MediaObjectID] && !onTrack[claim.MediaObjectID] {
			candidates = append(candidates, TrackFile{
				MediaObjectID: claim.MediaObjectID, Disc: int(claim.Disc), Track: int(claim.Track),
				Title: claim.Title, RecordingMBID: claim.RecordingMBID,
			})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	allParts, err := j.Desired.AllParts(ctx, item.WorkID)
	if err != nil {
		return err
	}
	recordingMBIDs, err := j.Desired.ExternalIDsFor(ctx, allParts, ProviderRecording)
	if err != nil {
		return err
	}
	parts := make([]Part, len(allParts))
	for index, part := range allParts {
		parts[index] = Part{PartID: part.ID, MediaRef: part.MediaRef, Title: part.Title.String, RecordingMBID: recordingMBIDs[part.ID]}
	}
	var aligned []library.AlignedFile
	for _, alignedPart := range Align(parts, candidates).Parts {
		if alignedPart.File != nil {
			aligned = append(aligned, library.AlignedFile{MediaObjectID: alignedPart.File.MediaObjectID, MediaRef: alignedPart.Part.MediaRef})
		}
	}
	if len(aligned) == 0 {
		return nil
	}
	return j.Desired.RecordAlignment(ctx, item.ID, item.WorkID, aligned, j.Clock.Now().Unix())
}

func anyDownloaded(files []library.MeasuredFile, downloaded map[int64]bool) bool {
	for _, file := range files {
		if downloaded[file.MediaObjectID] {
			return true
		}
	}
	return false
}

func allMeasured(files []library.MeasuredFile) bool {
	for _, file := range files {
		if !file.Measured {
			return false
		}
	}
	return true
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
	if !category.QualityUpgrades {
		return ItemStateComplete, true, nil // Lidarr's "Upgrades Allowed" off: every track here is enough
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
	override, overridden, err := j.Desired.ProfileOverride(ctx, item.ID)
	if err != nil {
		return "", false, err
	}
	for _, file := range files {
		if overridden {
			// An album's own profile is its whole wish: the file meets it or the album can still be upgraded.
			if fits, known, _ := quality.ProfileFits(snapshot, override, measuredFacts(file)); known {
				if !fits {
					return ItemStateUpgradable, true, nil
				}
				continue
			}
		}
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
		AudioCodec: file.AudioCodec, Extension: file.Extension, Lossless: file.Lossless,
		AudioBitrate: file.BitrateKbps * 1000, OverallBitrate: file.BitrateKbps * 1000,
		AudioLanguages: file.AudioLanguages, LyricsLanguages: file.LyricsLanguages,
	}}
}
