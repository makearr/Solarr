package movie

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
)

// CutoffJob settles a movie a download brought a second file for (the strictly better file stays, a tie keeps the owned
// one), then marks the movie UPGRADABLE or COMPLETE by what the probe measured.
type CutoffJob struct {
	Desired  library.Desired
	Reader   library.SeriesReader
	Settings *settings.Cache
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded, false for a
	// download that measured no better. Nil leaves every file in place.
	Recycle func(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error
	Logger  *slog.Logger
}

// CutoffPage implements cutoff.Check: a page of monitored movie items, or the one movie workID names.
func (j CutoffJob) CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error) {
	if workID == 0 {
		return j.Desired.CutoffPageOf(ctx, MediaType, afterID, limit)
	}
	var ofWork []library.CutoffItem
	for {
		page, err := j.Desired.CutoffPageOf(ctx, MediaType, afterID, limit)
		if err != nil {
			return nil, err
		}
		for _, item := range page {
			afterID = item.ID
			if item.WorkID == workID {
				ofWork = append(ofWork, item)
			}
		}
		if int64(len(page)) < limit {
			return ofWork, nil
		}
	}
}

// CheckCutoff implements cutoff.Check.
func (j CutoffJob) CheckCutoff(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (cutoff.Outcome, error) {
	settled, err := j.settle(ctx, snapshot, item)
	if err != nil {
		return cutoff.Outcome{}, err
	}
	state, decided, err := j.stateFor(ctx, snapshot, item)
	if err != nil {
		return cutoff.Outcome{}, err
	}
	return cutoff.Outcome{Replaced: settled, State: state, Decided: decided}, nil
}

// settle keeps, when a download brought a second file, the strictly better measured one and recycles the rest.
func (j CutoffJob) settle(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (int, error) {
	if j.Recycle == nil {
		return 0, nil
	}
	files, err := j.Reader.ItemFiles(ctx, item.ID)
	if err != nil || len(files) < 2 {
		return 0, err
	}
	downloaded, err := j.Desired.DownloadedFiles(ctx, item.WorkID)
	if err != nil {
		return 0, err
	}
	brought := false
	for _, file := range files {
		brought = brought || downloaded[file.File.MediaObjectID]
		if !file.Measured {
			return 0, nil
		}
	}
	if !brought {
		// Two files the person holds on purpose (editions) are not a contest.
		return 0, nil
	}
	rules, err := judge.LibraryProfiles(ctx, j.Desired, snapshot, item.ID, item.CategoryID, item.ProfileID)
	if errors.Is(err, judge.ErrNoQualityProfile) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	language := judge.LanguagePolicyFor(snapshot, item.CategoryID)
	keeper := files[0]
	for _, file := range files {
		if !downloaded[file.File.MediaObjectID] {
			keeper = file
			break
		}
	}
	for _, file := range files {
		if judge.StrictlyBetter(judge.EvaluateInOrder(rules, releasefacts.FromFile(file.File), language), judge.EvaluateInOrder(rules, releasefacts.FromFile(keeper.File), language)) {
			keeper = file
		}
	}
	recycled := 0
	for _, file := range files {
		if file.File.MediaObjectID == keeper.File.MediaObjectID {
			continue
		}
		if err := j.Recycle(ctx, file.File.MediaObjectID, item.CategoryID, !downloaded[file.File.MediaObjectID]); err != nil {
			return recycled, err
		}
		recycled++
	}
	return recycled, nil
}

// stateFor judges the movie's files; decided is false while any is unmeasured or there is none.
func (j CutoffJob) stateFor(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (string, bool, error) {
	category, found := snapshot.Category(item.CategoryID)
	if !found {
		return "", false, nil
	}
	if !category.QualityUpgrades {
		return ItemStateComplete, true, nil
	}
	files, err := j.Reader.ItemFiles(ctx, item.ID)
	if err != nil {
		return "", false, fmt.Errorf("movie: cutoff check item %d: %w", item.ID, err)
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
		judgement, judged := quality.Judge(snapshot, category, releasefacts.FromFile(file.File))
		if !judged {
			return ItemStateComplete, true, nil
		}
		if judgement.MeetsCutoff {
			return ItemStateComplete, true, nil
		}
	}
	return ItemStateUpgradable, true, nil
}
