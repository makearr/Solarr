package works

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
)

// Item states the cutoff check and the search move an item between.
const (
	StateUpgradable = "UPGRADABLE"
	StateComplete   = "COMPLETE"
	StatePartial    = "PARTIAL"
)

// File is one file of a wanted item as the cutoff check judges it. A file holding several parts (a double episode) is
// listed once for each.
type File struct {
	MediaObjectID int64
	// Ref is the part the file holds; empty for a work without parts.
	Ref       string
	AddedAt   int64
	Measured  bool
	Monitored bool
	Facts     score.Facts
}

// Cutoff settles the parts a download brought a second file for (the strictly better measured file stays, a tie keeps
// the owned one), then marks the item UPGRADABLE or COMPLETE by what its files are. Page and Files are the media type's.
type Cutoff struct {
	Desired library.Desired
	// Page is the next monitored items of the type after afterID, at most limit.
	Page func(ctx context.Context, afterID, limit int64) ([]library.CutoffItem, error)
	// Files are the item's files with what is known of them.
	Files func(ctx context.Context, itemID int64) ([]File, error)
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded, false for a
	// download that measured no better. Nil leaves every file in place.
	Recycle func(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error
	// Versions says a part's files are versions of it (a film's cuts, a book's formats): two held on purpose are no
	// contest, and one at the cutoff completes the item. Otherwise every file must reach it, as every episode must.
	Versions bool
	// Uncontested says a part's files are never weighed against each other: an audiobook's chapters are one set.
	Uncontested bool
	// NeverUpgrades marks an item complete once it has its files: a set of chapters is replaced by hand.
	NeverUpgrades bool
}

// CutoffPage implements cutoff.Check: a page of the type's items, or every item of the one work workID names.
func (c Cutoff) CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error) {
	if workID == 0 {
		return c.Page(ctx, afterID, limit)
	}
	var ofWork []library.CutoffItem
	for {
		page, err := c.Page(ctx, afterID, limit)
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

// CheckCutoff implements cutoff.Check: a second file is settled whatever the item's state; an item with gaps is not
// marked.
func (c Cutoff) CheckCutoff(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (cutoff.Outcome, error) {
	settled, err := c.settle(ctx, snapshot, item)
	if err != nil {
		return cutoff.Outcome{}, err
	}
	if item.State == StatePartial {
		return cutoff.Outcome{Replaced: settled}, nil
	}
	state, decided, tally, err := c.stateFor(ctx, snapshot, item)
	if err != nil {
		return cutoff.Outcome{}, err
	}
	return cutoff.Outcome{Replaced: settled, State: state, Decided: decided, Judged: tally.Judged, BelowCutoff: tally.BelowCutoff}, nil
}

// settle keeps, per part a download brought a second file for, the strictly better measured file and recycles the
// rest; answers how many were recycled.
func (c Cutoff) settle(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (int, error) {
	if c.Recycle == nil || c.Uncontested {
		return 0, nil
	}
	files, err := c.Files(ctx, item.ID)
	if err != nil {
		return 0, err
	}
	byPart := map[string][]File{}
	var order []string
	covers := map[int64]int{}
	for _, file := range files {
		if byPart[file.Ref] == nil {
			order = append(order, file.Ref)
		}
		byPart[file.Ref] = append(byPart[file.Ref], file)
		covers[file.MediaObjectID]++
	}
	contested := false
	for _, part := range byPart {
		contested = contested || len(part) > 1
	}
	if !contested {
		return 0, nil
	}
	downloaded, err := c.Desired.DownloadedFiles(ctx, item.WorkID)
	if err != nil {
		return 0, err
	}
	rules, err := judge.LibraryProfiles(ctx, c.Desired, snapshot, item.ID, item.CategoryID, item.ProfileID)
	if errors.Is(err, judge.ErrNoQualityProfile) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	language := judge.LanguagePolicyFor(snapshot, item.CategoryID)
	// A file holding several parts goes only once it lost every part it holds: beaten on one, it is still another's only file.
	lost := map[int64]int{}
	var losers []File
	for _, ref := range order {
		part := byPart[ref]
		if len(part) < 2 || !anyBrought(part, downloaded) || !allMeasured(part) {
			continue
		}
		held := 0
		keeper := part[0]
		for _, file := range part {
			if !downloaded[file.MediaObjectID] {
				if held == 0 {
					keeper = file
				}
				held++
			}
		}
		for _, file := range part {
			if judge.StrictlyBetter(judge.EvaluateInOrder(rules, file.Facts, language), judge.EvaluateInOrder(rules, keeper.Facts, language)) {
				keeper = file
			}
		}
		for _, file := range part {
			// Two or more held versions are kept on purpose: a download contests only other downloads then.
			if file.MediaObjectID == keeper.MediaObjectID || (c.Versions && held > 1 && !downloaded[file.MediaObjectID]) {
				continue
			}
			lost[file.MediaObjectID]++
			if lost[file.MediaObjectID] == covers[file.MediaObjectID] {
				losers = append(losers, file)
			}
		}
	}
	recycled := 0
	for _, file := range losers {
		if err := c.Recycle(ctx, file.MediaObjectID, item.CategoryID, !downloaded[file.MediaObjectID]); err != nil {
			return recycled, err
		}
		recycled++
	}
	return recycled, nil
}

func anyBrought(files []File, downloaded map[int64]bool) bool {
	for _, file := range files {
		if downloaded[file.MediaObjectID] {
			return true
		}
	}
	return false
}

func allMeasured(files []File) bool {
	for _, file := range files {
		if !file.Measured {
			return false
		}
	}
	return true
}

// stateFor judges the item's monitored files; decided is false while any file is unmeasured or there is none. The
// tally is empty unless every monitored file was judged.
func (c Cutoff) stateFor(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (string, bool, cutoff.Tally, error) {
	category, found := snapshot.Category(item.CategoryID)
	if !found {
		return "", false, cutoff.Tally{}, nil
	}
	if c.NeverUpgrades {
		return StateComplete, true, cutoff.Tally{}, nil
	}
	files, err := c.Files(ctx, item.ID)
	if err != nil {
		return "", false, cutoff.Tally{}, fmt.Errorf("cutoff check item %d: %w", item.ID, err)
	}
	if !category.QualityUpgrades {
		// Counted for the statistics page; the item stays complete because the library never upgrades.
		return StateComplete, true, c.tally(snapshot, category, files), nil
	}
	if len(files) == 0 || !allMeasured(files) {
		return "", false, cutoff.Tally{}, nil
	}
	judgedAny, unmet := false, false
	var tally cutoff.Tally
	for _, file := range files {
		if !file.Monitored {
			continue
		}
		judgement, judged := quality.Judge(snapshot, category, file.Facts)
		switch {
		case !judged:
			return StateComplete, true, cutoff.Tally{}, nil
		case c.Versions && judgement.MeetsCutoff:
			return StateComplete, true, cutoff.Tally{}, nil
		case !c.Versions && !judgement.MeetsCutoff:
			unmet = true
			tally.BelowCutoff++
		}
		judgedAny = true
		tally.Judged++
	}
	switch {
	case c.Versions:
		tally = cutoff.Tally{}
		if judgedAny {
			return StateUpgradable, true, tally, nil
		}
	case unmet:
		return StateUpgradable, true, tally, nil
	}
	return StateComplete, true, tally, nil
}

// tally counts the monitored files the profile can judge and those that miss the cutoff.
func (c Cutoff) tally(snapshot *settings.Snapshot, category settings.Category, files []File) cutoff.Tally {
	var tally cutoff.Tally
	if c.Versions {
		return tally
	}
	for _, file := range files {
		if !file.Monitored || !file.Measured {
			continue
		}
		if judgement, judged := quality.Judge(snapshot, category, file.Facts); judged {
			tally.Judged++
			if !judgement.MeetsCutoff {
				tally.BelowCutoff++
			}
		}
	}
	return tally
}

// BelowCutoff are the item's files that miss the library's cutoff, by part, the first of each: what an upgrade
// replaces. A file added after newest (Unix seconds), or unmeasured, has not settled and is left alone.
func BelowCutoff(snapshot *settings.Snapshot, categoryID int64, files []File, newest int64) map[string]File {
	category, found := snapshot.Category(categoryID)
	if !found || !category.QualityUpgrades {
		return nil
	}
	below := map[string]File{}
	for _, file := range files {
		if !file.Measured || file.AddedAt > newest {
			continue
		}
		if _, has := below[file.Ref]; has {
			continue
		}
		if judged, ok := quality.Judge(snapshot, category, file.Facts); ok && !judged.MeetsCutoff {
			below[file.Ref] = file
		}
	}
	return below
}
