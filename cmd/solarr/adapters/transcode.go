package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

// Transcode satisfies apiserver.TranscodePipeline over the conversion pipeline, its items and the savings statistics.
type Transcode struct {
	Library  library.TranscodeItems
	Service  *transcoder.Service
	Stats    statistics.Transcodes
	Settings *settings.Cache
}

var transcodeKinds = []string{"video", "audio"}

func countsView(counts map[library.TranscodeState]int64) apiserver.TranscodeCountsView {
	return apiserver.TranscodeCountsView{
		Needed: counts[library.TranscodeNeeded], Running: counts[library.TranscodeRunning], Done: counts[library.TranscodeDone],
		NotNeeded: counts[library.TranscodeNotNeeded], Failed: counts[library.TranscodeFailed], Blocked: counts[library.TranscodeBlocked],
	}
}

func savingsView(total statistics.TranscodeTotal) apiserver.TranscodeSavingsView {
	return apiserver.TranscodeSavingsView{Files: total.Files, OriginalBytes: total.OriginalBytes, ResultBytes: total.ResultBytes, EncodeSeconds: total.EncodeSeconds}
}

func (a Transcode) Summary(ctx context.Context) (apiserver.TranscodeSummaryView, error) {
	libraries, err := a.Settings.TranscodeLibraries(ctx)
	if err != nil {
		return apiserver.TranscodeSummaryView{}, err
	}
	perKind, all, err := a.Stats.Totals(ctx)
	if err != nil {
		return apiserver.TranscodeSummaryView{}, err
	}
	overall, err := a.Library.Counts(ctx, 0)
	if err != nil {
		return apiserver.TranscodeSummaryView{}, err
	}
	out := apiserver.TranscodeSummaryView{Enabled: a.Settings.Snapshot().TranscodeGlobal().Enabled, Totals: savingsView(all), Counts: countsView(overall)}
	for _, one := range libraries {
		if !one.Supported() {
			continue
		}
		counts, err := a.Library.Counts(ctx, one.CategoryID)
		if err != nil {
			return apiserver.TranscodeSummaryView{}, err
		}
		view := apiserver.TranscodeLibraryView{CategoryID: one.CategoryID, TargetID: one.Target.ID, Name: one.Name, Enabled: one.Enabled, Counts: countsView(counts)}
		for _, total := range perKind {
			if total.CategoryID == one.CategoryID {
				view.Savings.Files += total.Files
				view.Savings.OriginalBytes += total.OriginalBytes
				view.Savings.ResultBytes += total.ResultBytes
				view.Savings.EncodeSeconds += total.EncodeSeconds
			}
		}
		if stored, err := a.Settings.TranscodeSetup(ctx, one.Target.ID); err == nil {
			view.Warnings = settings.TranscodeWarnings(stored.Setup, *one.Target)
		}
		if ratios, err := a.Library.DoneRatios(ctx, one.CategoryID); err == nil {
			if warning, ok := settings.TranscodeResultsWarning(ratios, *one.Target); ok {
				view.Warnings = append(view.Warnings, warning)
			}
		}
		out.Libraries = append(out.Libraries, view)
	}
	for _, kind := range transcodeKinds {
		health, err := a.Library.Health(ctx, kind)
		if err != nil {
			return apiserver.TranscodeSummaryView{}, err
		}
		view := apiserver.TranscodeHealthView{Kind: kind, Mode: health.Mode, LastError: health.LastError, ConsecutiveFailures: health.ConsecutiveFailures}
		if health.BackoffUntil != 0 {
			view.BackoffUntil = time.Unix(health.BackoffUntil, 0)
		}
		out.Health = append(out.Health, view)
	}
	if out.OriginalsBytes, err = a.Library.OriginalsKept(ctx); err != nil {
		return apiserver.TranscodeSummaryView{}, err
	}
	var nextExpiry int64
	if out.OriginalsCount, nextExpiry, err = a.Library.OriginalsHeld(ctx); err != nil {
		return apiserver.TranscodeSummaryView{}, err
	}
	if nextExpiry != 0 {
		out.OriginalsNextExpiry = time.Unix(nextExpiry, 0)
	}
	out.DiskFullRoots = a.Service.BlockedRoots()
	return out, nil
}

func itemView(item library.TranscodeItem, relPath string) apiserver.TranscodeItemView {
	return apiserver.TranscodeItemView{
		MediaObjectID: item.MediaObjectID, CategoryID: item.CategoryID, Path: relPath, Kind: item.Kind, State: string(item.State), Lane: item.Lane,
		ReasonsJSON: item.ReasonsJSON, WarningsJSON: item.WarningsJSON, PlanJSON: item.PlanJSON, Attempts: item.Attempts, Pass: item.Pass,
		Interrupted: item.Interrupted, FailureCode: item.FailureCode, LastError: item.LastError, Engine: item.Engine,
		OriginalSizeBytes: item.OriginalSizeBytes, ResultSizeBytes: item.ResultSizeBytes, ResultRatioPct: item.ResultRatioPct, EncodeSeconds: item.EncodeSeconds,
		FinishedAt: item.FinishedAt, Restorable: item.State == library.TranscodeDone && item.OriginalRecycleID != 0,
	}
}

func (a Transcode) Items(ctx context.Context, query apiserver.TranscodeItemsQuery) ([]apiserver.TranscodeItemView, error) {
	listed, err := a.Library.List(ctx, library.TranscodeFilter{
		CategoryID: query.CategoryID, State: library.TranscodeState(query.State), Kind: query.Kind, Search: query.Search, AfterID: query.AfterID, PageSize: query.Size,
	})
	if err != nil {
		return nil, err
	}
	views := make([]apiserver.TranscodeItemView, len(listed))
	var recycleIDs []int64
	for i, one := range listed {
		views[i] = itemView(one.Item, one.RelPath)
		if views[i].OriginalSizeBytes == 0 {
			views[i].OriginalSizeBytes = one.SizeBytes
		}
		if views[i].Restorable {
			recycleIDs = append(recycleIDs, one.Item.OriginalRecycleID)
		}
	}
	expiries, err := a.Library.OriginalExpiries(ctx, recycleIDs)
	if err != nil {
		return nil, err
	}
	for i, one := range listed {
		a.markRestorable(&views[i], one.Item.OriginalRecycleID, expiries)
	}
	return views, nil
}

func (a Transcode) Item(ctx context.Context, id int64) (apiserver.TranscodeItemView, bool, error) {
	item, found, err := a.Library.Get(ctx, id)
	if err != nil || !found {
		return apiserver.TranscodeItemView{}, false, err
	}
	paths, err := a.Library.Paths(ctx, id)
	if err != nil {
		return apiserver.TranscodeItemView{}, false, err
	}
	relPath := ""
	if len(paths) > 0 {
		relPath = paths[0].RelPath
	}
	view := itemView(item, relPath)
	if fresh, ok, err := a.Service.FreshPlan(ctx, id); err != nil {
		return apiserver.TranscodeItemView{}, false, err
	} else if ok {
		view.ReasonsJSON, view.PlanJSON, view.WarningsJSON = fresh.ReasonsJSON, fresh.PlanJSON, fresh.WarningsJSON
	}
	if view.Restorable {
		expiries, err := a.Library.OriginalExpiries(ctx, []int64{item.OriginalRecycleID})
		if err != nil {
			return apiserver.TranscodeItemView{}, false, err
		}
		a.markRestorable(&view, item.OriginalRecycleID, expiries)
	}
	return view, true, nil
}

// markRestorable dates the restore window, or withdraws it when the bin no longer holds the original.
func (Transcode) markRestorable(view *apiserver.TranscodeItemView, recycleID int64, expiries map[int64]int64) {
	if !view.Restorable {
		return
	}
	expiry, kept := expiries[recycleID]
	view.Restorable = kept
	if kept {
		view.RestorableUntil = time.Unix(expiry, 0)
	}
}

func (a Transcode) Retry(ctx context.Context, query apiserver.TranscodeRetryQuery) (int64, error) {
	count, err := a.Service.Retry(ctx, transcoder.Selection{
		IDs: query.IDs, State: library.TranscodeState(query.State), CategoryID: query.CategoryID, Kind: query.Kind, Search: query.Search, Excluded: query.Excluded,
	}, transcoder.Override{Quality: query.Quality, VideoBitrateKbps: query.VideoBitrateKbps, Preset: query.Preset, Engine: query.Engine}, !query.Preview)
	if errors.Is(err, transcoder.ErrBadOverride) {
		return 0, apiserver.ErrTranscodeBadRetry
	}
	return count, err
}

func (a Transcode) Restore(ctx context.Context, id int64) error {
	err := a.Service.Restore(ctx, id)
	switch {
	case errors.Is(err, library.ErrNoTranscodeItem):
		return apiserver.ErrTranscodeNoItem
	case errors.Is(err, transcoder.ErrOriginalGone):
		return apiserver.ErrTranscodeOriginalGone
	case errors.Is(err, transcoder.ErrNotRestorable):
		return apiserver.ErrTranscodeNotRestorable
	case errors.Is(err, transcoder.ErrOriginalBlocked):
		return apiserver.ErrTranscodePlaceTaken
	case errors.Is(err, transcoder.ErrRootUnavailable):
		return apiserver.ErrTranscodeFolder
	}
	return err
}

func (a Transcode) ResumeHealth(ctx context.Context, kind string) error {
	return a.Service.ResumeHealth(ctx, kind)
}
