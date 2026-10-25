package book

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
)

// CutoffJob settles an ebook a download brought a second file for (the strictly better format stays, a tie keeps the
// owned one), then marks the book UPGRADABLE or COMPLETE. An audiobook's chapters are one set: they are judged, never
// contested one against another.
type CutoffJob struct {
	Format   Format
	Desired  library.Desired
	Reader   library.SeriesReader
	Physical library.Physical
	Settings *settings.Cache
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded. Nil leaves
	// every file in place.
	Recycle func(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error
	Logger  *slog.Logger
}

// CutoffPage implements cutoff.Check: a page of monitored book items, or the one book workID names.
func (j CutoffJob) CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error) {
	if workID == 0 {
		return j.Desired.CutoffPageOf(ctx, j.Format.MediaType, afterID, limit)
	}
	var ofWork []library.CutoffItem
	for {
		page, err := j.Desired.CutoffPageOf(ctx, j.Format.MediaType, afterID, limit)
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
	files, err := fileFacts(ctx, j.Format, j.Reader, j.Physical, item.ID)
	if err != nil {
		return cutoff.Outcome{}, fmt.Errorf("book: cutoff check item %d: %w", item.ID, err)
	}
	settled := 0
	if !j.Format.ByFolder {
		if settled, err = j.settle(ctx, snapshot, item, files); err != nil {
			return cutoff.Outcome{}, err
		}
		if settled > 0 {
			if files, err = fileFacts(ctx, j.Format, j.Reader, j.Physical, item.ID); err != nil {
				return cutoff.Outcome{}, err
			}
		}
	}
	state, decided := j.stateFor(snapshot, item, files)
	return cutoff.Outcome{Replaced: settled, State: state, Decided: decided}, nil
}

// settle keeps, when a download brought a second ebook file, the strictly better one and recycles the rest.
func (j CutoffJob) settle(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem, files []heldFile) (int, error) {
	if j.Recycle == nil || len(files) < 2 {
		return 0, nil
	}
	downloaded, err := j.Desired.DownloadedFiles(ctx, item.WorkID)
	if err != nil {
		return 0, err
	}
	held, brought := 0, false
	for _, file := range files {
		brought = brought || downloaded[file.mediaObjectID]
		if !downloaded[file.mediaObjectID] {
			held++
		}
		if !file.measured {
			return 0, nil
		}
	}
	// Two files the person holds on purpose (an EPUB and a PDF) are not a contest.
	if !brought || held > 1 {
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
		if !downloaded[file.mediaObjectID] {
			keeper = file
			break
		}
	}
	for _, file := range files {
		if judge.StrictlyBetter(judge.EvaluateInOrder(rules, file.facts, language), judge.EvaluateInOrder(rules, keeper.facts, language)) {
			keeper = file
		}
	}
	recycled := 0
	for _, file := range files {
		if file.mediaObjectID == keeper.mediaObjectID {
			continue
		}
		if err := j.Recycle(ctx, file.mediaObjectID, item.CategoryID, !downloaded[file.mediaObjectID]); err != nil {
			return recycled, err
		}
		recycled++
	}
	return recycled, nil
}

// stateFor judges the book's files; decided is false while any is unmeasured or there is none.
func (j CutoffJob) stateFor(snapshot *settings.Snapshot, item library.CutoffItem, files []heldFile) (string, bool) {
	category, found := snapshot.Category(item.CategoryID)
	if !found {
		return "", false
	}
	if !category.QualityUpgrades || j.Format.ByFolder {
		return ItemStateComplete, true
	}
	if len(files) == 0 {
		return "", false
	}
	for _, file := range files {
		if !file.measured {
			return "", false
		}
	}
	for _, file := range files {
		judgement, judged := quality.Judge(snapshot, category, file.facts)
		if !judged || judgement.MeetsCutoff {
			return ItemStateComplete, true
		}
	}
	return ItemStateUpgradable, true
}
