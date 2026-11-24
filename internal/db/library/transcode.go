package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// TranscodeState is where a file stands in the conversion pipeline.
type TranscodeState string

// The states; the counters mirror one metric per state.
const (
	TranscodeNeeded    TranscodeState = "needed"
	TranscodeRunning   TranscodeState = "running"
	TranscodeDone      TranscodeState = "done"
	TranscodeNotNeeded TranscodeState = "not_needed"
	TranscodeFailed    TranscodeState = "failed"
	TranscodeBlocked   TranscodeState = "blocked"
)

// TranscodeStates lists every state, for seeding counters.
var TranscodeStates = []TranscodeState{TranscodeNeeded, TranscodeRunning, TranscodeDone, TranscodeNotNeeded, TranscodeFailed, TranscodeBlocked}

// transcodeChunk bounds one transaction (200 rows).
const transcodeChunk = 200

// ErrTranscodeState is a transition refused because the item was not in the state the caller expected.
var ErrTranscodeState = errors.New("library: the transcode item is not in the expected state")

// ErrNoTranscodeItem is an item asked for that was never judged.
var ErrNoTranscodeItem = errors.New("library: no such transcode item")

// TranscodeMetric is the counter metric of a state, in the scope "category:<id>".
func TranscodeMetric(state TranscodeState) string { return "transcode." + string(state) }

// nullableID reads zero as no row.
func nullableID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

func transcodeScope(categoryID int64) string { return fmt.Sprintf("category:%d", categoryID) }

// TranscodeItem is one judged audio or video file.
type TranscodeItem struct {
	MediaObjectID, CategoryID                                         int64
	Kind                                                              string
	State                                                             TranscodeState
	Lane                                                              string
	TargetID, SetupRevision                                           int64
	ReasonsJSON, PlanJSON, WarningsJSON, OverrideJSON                 string
	Attempts, Pass                                                    int64
	Interrupted                                                       bool
	FailureCode, LastError                                            string
	FoundAt, LastRunSeq                                               int64
	ReplaceStage, WorkPath                                            string
	OriginalRecycleID                                                 int64
	OriginalSizeBytes, ResultSizeBytes, ResultRatioPct, EncodeSeconds int64
	Engine                                                            string
	FinishedAt, UpdatedAt                                             int64
}

// JudgedTranscodeItem is what the analysis decided about one file.
type JudgedTranscodeItem struct {
	MediaObjectID, CategoryID, TargetID, SetupRevision int64
	Kind, Lane                                         string
	State                                              TranscodeState
	ReasonsJSON, PlanJSON, WarningsJSON                string
}

// TranscodeItems keeps the judged files and the counters that mirror their states.
type TranscodeItems struct {
	store *store.Store
	clock clock.Clock
}

// NewTranscodeItems wraps an opened library database.
func NewTranscodeItems(opened *store.Store, cl clock.Clock) TranscodeItems {
	return TranscodeItems{store: opened, clock: cl}
}

func adjustState(ctx context.Context, queries *librarydb.Queries, categoryID int64, state TranscodeState, delta, now int64) error {
	return queries.AdjustCounter(ctx, librarydb.AdjustCounterParams{Scope: transcodeScope(categoryID), Metric: TranscodeMetric(state), Value: delta, UpdatedAt: now})
}

// Judge records the analysis of files; a running item is left as it is. Counters move in the same transaction.
func (t TranscodeItems) Judge(ctx context.Context, judged []JudgedTranscodeItem) error {
	now := t.clock.Now().Unix()
	for start := 0; start < len(judged); start += transcodeChunk {
		chunk := judged[start:min(start+transcodeChunk, len(judged))]
		err := t.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, item := range chunk {
				if err := t.judgeOne(ctx, queries, item, now); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: judge transcode items: %w", err)
		}
	}
	return nil
}

func (t TranscodeItems) judgeOne(ctx context.Context, queries *librarydb.Queries, item JudgedTranscodeItem, now int64) error {
	existing, err := queries.GetTranscodeItem(ctx, item.MediaObjectID)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if found && TranscodeState(existing.State) == TranscodeRunning {
		return nil
	}
	err = queries.UpsertTranscodeItem(ctx, librarydb.UpsertTranscodeItemParams{
		MediaObjectID: item.MediaObjectID, CategoryID: item.CategoryID, Kind: item.Kind, State: string(item.State), Lane: item.Lane,
		TargetID: item.TargetID, SetupRevision: item.SetupRevision, ReasonsJson: item.ReasonsJSON, PlanJson: item.PlanJSON,
		WarningsJson: item.WarningsJSON, FoundAt: now, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	if found {
		if err := adjustState(ctx, queries, existing.CategoryID, TranscodeState(existing.State), -1, now); err != nil {
			return err
		}
	}
	return adjustState(ctx, queries, item.CategoryID, item.State, 1, now)
}

func itemOf(row librarydb.TranscodeItem) TranscodeItem {
	return TranscodeItem{
		MediaObjectID: row.MediaObjectID, CategoryID: row.CategoryID, Kind: row.Kind, State: TranscodeState(row.State), Lane: row.Lane,
		TargetID: row.TargetID, SetupRevision: row.SetupRevision, ReasonsJSON: row.ReasonsJson, PlanJSON: row.PlanJson,
		WarningsJSON: row.WarningsJson, OverrideJSON: row.OverrideJson, Attempts: row.Attempts, Pass: row.Pass, Interrupted: row.Interrupted == 1,
		FailureCode: row.FailureCode, LastError: row.LastError, FoundAt: row.FoundAt, LastRunSeq: row.LastRunSeq,
		ReplaceStage: row.ReplaceStage, WorkPath: row.WorkPath, OriginalRecycleID: row.OriginalRecycleID.Int64,
		OriginalSizeBytes: row.OriginalSizeBytes, ResultSizeBytes: row.ResultSizeBytes, ResultRatioPct: row.ResultRatioPct, Engine: row.Engine,
		EncodeSeconds: row.EncodeSeconds, FinishedAt: row.FinishedAt.Int64, UpdatedAt: row.UpdatedAt,
	}
}

// Get answers one item; false when the file was never judged.
func (t TranscodeItems) Get(ctx context.Context, mediaObjectID int64) (TranscodeItem, bool, error) {
	row, err := librarydb.New(t.store.Read()).GetTranscodeItem(ctx, mediaObjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return TranscodeItem{}, false, nil
	}
	if err != nil {
		return TranscodeItem{}, false, fmt.Errorf("library: transcode item %d: %w", mediaObjectID, err)
	}
	return itemOf(row), true, nil
}

// Transition changes an item from one state to another, applying mutate to its mutable fields, and moves the two counters in the same
// transaction; it refuses when the item is not in from.
func (t TranscodeItems) Transition(ctx context.Context, mediaObjectID int64, from, to TranscodeState, mutate func(*TranscodeItem)) error {
	now := t.clock.Now().Unix()
	err := t.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		row, err := queries.GetTranscodeItem(ctx, mediaObjectID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoTranscodeItem
		}
		if err != nil {
			return err
		}
		if TranscodeState(row.State) != from {
			return ErrTranscodeState
		}
		item := itemOf(row)
		item.State = to
		if mutate != nil {
			mutate(&item)
		}
		item.State = to
		interrupted := int64(0)
		if item.Interrupted {
			interrupted = 1
		}
		err = queries.SetTranscodeItemState(ctx, librarydb.SetTranscodeItemStateParams{
			State: string(item.State), Lane: item.Lane, OverrideJson: item.OverrideJSON, Attempts: item.Attempts, Pass: item.Pass,
			Interrupted: interrupted, FailureCode: item.FailureCode, LastError: item.LastError, LastRunSeq: item.LastRunSeq, UpdatedAt: now,
			MediaObjectID: mediaObjectID,
		})
		if err != nil {
			return err
		}
		if from == to {
			return nil
		}
		if err := adjustState(ctx, queries, item.CategoryID, from, -1, now); err != nil {
			return err
		}
		return adjustState(ctx, queries, item.CategoryID, to, 1, now)
	})
	if err != nil {
		return fmt.Errorf("library: transcode item %d %s to %s: %w", mediaObjectID, from, to, err)
	}
	return nil
}

// TranscodeResult is what a finished conversion leaves on its item.
type TranscodeResult struct {
	OriginalRecycleID                                                 int64
	OriginalSizeBytes, ResultSizeBytes, ResultRatioPct, EncodeSeconds int64
	Engine                                                            string
	FinishedAt                                                        int64
}

// SetResult records the sizes, the engine and the recycled original of a finished conversion.
func (t TranscodeItems) SetResult(ctx context.Context, mediaObjectID int64, result TranscodeResult) error {
	return t.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetTranscodeItemResult(ctx, librarydb.SetTranscodeItemResultParams{
			OriginalRecycleID: nullableID(result.OriginalRecycleID), OriginalSizeBytes: result.OriginalSizeBytes, ResultSizeBytes: result.ResultSizeBytes,
			ResultRatioPct: result.ResultRatioPct, Engine: result.Engine, EncodeSeconds: result.EncodeSeconds,
			FinishedAt: sql.NullInt64{Int64: result.FinishedAt, Valid: result.FinishedAt != 0}, UpdatedAt: t.clock.Now().Unix(), MediaObjectID: mediaObjectID,
		})
	})
}

// SetJournal records the stage of a replace a restart must finish or undo; an empty stage clears it.
func (t TranscodeItems) SetJournal(ctx context.Context, mediaObjectID int64, stage, workPath string, recycleID int64) error {
	return t.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetTranscodeItemJournal(ctx, librarydb.SetTranscodeItemJournalParams{
			ReplaceStage: stage, WorkPath: workPath, OriginalRecycleID: nullableID(recycleID), UpdatedAt: t.clock.Now().Unix(), MediaObjectID: mediaObjectID,
		})
	})
}

// ObjectToAnalyze is a file the analysis has not judged against the current target and setup.
type ObjectToAnalyze struct {
	MediaObjectID, SizeBytes, RootFolderID int64
	Kind, RelPath                          string
}

// ObjectsToAnalyze answers one page of files under the roots, after the given id, that need judging.
func (t TranscodeItems) ObjectsToAnalyze(ctx context.Context, rootIDs []int64, targetID, setupRevision, afterID, pageSize int64) ([]ObjectToAnalyze, error) {
	encoded, err := json.Marshal(rootIDs)
	if err != nil {
		return nil, err
	}
	rows, err := librarydb.New(t.store.Read()).ListObjectsToAnalyze(ctx, librarydb.ListObjectsToAnalyzeParams{
		AfterID: afterID, RootIds: string(encoded), TargetID: targetID, SetupRevision: setupRevision, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: files to analyse: %w", err)
	}
	out := make([]ObjectToAnalyze, len(rows))
	for i, row := range rows {
		out[i] = ObjectToAnalyze{MediaObjectID: row.ID, SizeBytes: row.SizeBytes, RootFolderID: row.RootFolderID, Kind: row.Kind.String, RelPath: row.RelPath}
	}
	return out, nil
}

// TranscodeOrder is how a library's needed files are taken.
type TranscodeOrder string

// The orders a library can be worked in.
const (
	OrderAlphabetical TranscodeOrder = "alphabetical"
	OrderLargest      TranscodeOrder = "largest"
	OrderSmallest     TranscodeOrder = "smallest"
	OrderOldest       TranscodeOrder = "oldest"
	OrderNewest       TranscodeOrder = "newest"
	OrderFound        TranscodeOrder = "found"
)

// TranscodePick is a candidate for the dispatcher.
type TranscodePick struct {
	MediaObjectID, LastRunSeq, RootFolderID int64
	Interrupted                             bool
	RelPath                                 string
}

// Pick answers up to limit needed files of one library and kind in the order, interrupted ones first.
func (t TranscodeItems) Pick(ctx context.Context, order TranscodeOrder, categoryID int64, kind string, limit int64) ([]TranscodePick, error) {
	reader := librarydb.New(t.store.Read())
	var picks []TranscodePick
	add := func(id, seq, root, interrupted int64, path string) {
		picks = append(picks, TranscodePick{MediaObjectID: id, LastRunSeq: seq, RootFolderID: root, Interrupted: interrupted == 1, RelPath: path})
	}
	var err error
	switch order {
	case OrderLargest:
		var rows []librarydb.PickNeededLargestRow
		rows, err = reader.PickNeededLargest(ctx, librarydb.PickNeededLargestParams{CategoryID: categoryID, Kind: kind, Limit: limit})
		for _, r := range rows {
			add(r.MediaObjectID, r.LastRunSeq, r.RootFolderID, r.Interrupted, r.RelPath)
		}
	case OrderSmallest:
		var rows []librarydb.PickNeededSmallestRow
		rows, err = reader.PickNeededSmallest(ctx, librarydb.PickNeededSmallestParams{CategoryID: categoryID, Kind: kind, Limit: limit})
		for _, r := range rows {
			add(r.MediaObjectID, r.LastRunSeq, r.RootFolderID, r.Interrupted, r.RelPath)
		}
	case OrderOldest:
		var rows []librarydb.PickNeededOldestRow
		rows, err = reader.PickNeededOldest(ctx, librarydb.PickNeededOldestParams{CategoryID: categoryID, Kind: kind, Limit: limit})
		for _, r := range rows {
			add(r.MediaObjectID, r.LastRunSeq, r.RootFolderID, r.Interrupted, r.RelPath)
		}
	case OrderNewest:
		var rows []librarydb.PickNeededNewestRow
		rows, err = reader.PickNeededNewest(ctx, librarydb.PickNeededNewestParams{CategoryID: categoryID, Kind: kind, Limit: limit})
		for _, r := range rows {
			add(r.MediaObjectID, r.LastRunSeq, r.RootFolderID, r.Interrupted, r.RelPath)
		}
	case OrderFound:
		var rows []librarydb.PickNeededFoundRow
		rows, err = reader.PickNeededFound(ctx, librarydb.PickNeededFoundParams{CategoryID: categoryID, Kind: kind, Limit: limit})
		for _, r := range rows {
			add(r.MediaObjectID, r.LastRunSeq, r.RootFolderID, r.Interrupted, r.RelPath)
		}
	default:
		var rows []librarydb.PickNeededAlphabeticalRow
		rows, err = reader.PickNeededAlphabetical(ctx, librarydb.PickNeededAlphabeticalParams{CategoryID: categoryID, Kind: kind, Limit: limit})
		for _, r := range rows {
			add(r.MediaObjectID, r.LastRunSeq, r.RootFolderID, r.Interrupted, r.RelPath)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("library: pick transcode items: %w", err)
	}
	return picks, nil
}

// PickFarAway answers needed files from the far end of the alphabet outside one top-level folder (prefix ends in "/"; empty avoids nothing).
func (t TranscodeItems) PickFarAway(ctx context.Context, categoryID int64, kind string, avoidRootID int64, avoidPrefix string, limit int64) ([]TranscodePick, error) {
	rows, err := librarydb.New(t.store.Read()).PickNeededFarAway(ctx, librarydb.PickNeededFarAwayParams{
		CategoryID: categoryID, Kind: kind, AvoidRootID: avoidRootID, AvoidPrefix: avoidPrefix, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("library: pick far away transcode items: %w", err)
	}
	picks := make([]TranscodePick, len(rows))
	for i, r := range rows {
		picks[i] = TranscodePick{MediaObjectID: r.MediaObjectID, LastRunSeq: r.LastRunSeq, RootFolderID: r.RootFolderID, Interrupted: r.Interrupted == 1, RelPath: r.RelPath}
	}
	return picks, nil
}

// TranscodeFilter narrows the list the API shows; zero fields match everything.
type TranscodeFilter struct {
	CategoryID int64
	State      TranscodeState
	Kind       string
	Search     string
	AfterID    int64
	PageSize   int64
}

// TranscodeListed is an item with where its file lies.
type TranscodeListed struct {
	Item         TranscodeItem
	RootFolderID int64
	RelPath      string
}

// List answers one page of items by id.
func (t TranscodeItems) List(ctx context.Context, filter TranscodeFilter) ([]TranscodeListed, error) {
	rows, err := librarydb.New(t.store.Read()).ListTranscodeItems(ctx, librarydb.ListTranscodeItemsParams{
		AfterID: filter.AfterID, CategoryID: filter.CategoryID, State: string(filter.State), Kind: filter.Kind, Search: filter.Search, PageSize: filter.PageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list transcode items: %w", err)
	}
	out := make([]TranscodeListed, len(rows))
	for i, row := range rows {
		out[i] = TranscodeListed{RootFolderID: row.RootFolderID, RelPath: row.RelPath, Item: itemOf(librarydb.TranscodeItem{
			MediaObjectID: row.MediaObjectID, CategoryID: row.CategoryID, Kind: row.Kind, State: row.State, Lane: row.Lane, TargetID: row.TargetID,
			SetupRevision: row.SetupRevision, ReasonsJson: row.ReasonsJson, PlanJson: row.PlanJson, WarningsJson: row.WarningsJson,
			OverrideJson: row.OverrideJson, Attempts: row.Attempts, Pass: row.Pass, Interrupted: row.Interrupted, FailureCode: row.FailureCode,
			LastError: row.LastError, FoundAt: row.FoundAt, LastRunSeq: row.LastRunSeq, ReplaceStage: row.ReplaceStage, WorkPath: row.WorkPath,
			OriginalRecycleID: row.OriginalRecycleID, OriginalSizeBytes: row.OriginalSizeBytes, ResultSizeBytes: row.ResultSizeBytes,
			ResultRatioPct: row.ResultRatioPct, Engine: row.Engine, EncodeSeconds: row.EncodeSeconds, FinishedAt: row.FinishedAt, UpdatedAt: row.UpdatedAt,
		})}
	}
	return out, nil
}

// Running answers the items a restart must settle: those that were converting when the process ended.
func (t TranscodeItems) Running(ctx context.Context) ([]TranscodeItem, error) {
	rows, err := librarydb.New(t.store.Read()).ListRunningTranscodeItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: running transcode items: %w", err)
	}
	out := make([]TranscodeItem, len(rows))
	for i, row := range rows {
		out[i] = TranscodeItem{MediaObjectID: row.MediaObjectID, CategoryID: row.CategoryID, Kind: row.Kind, State: TranscodeRunning, WorkPath: row.WorkPath, LastRunSeq: row.LastRunSeq}
	}
	return out, nil
}

// Journalled answers the items with a replace the process ended in the middle of.
func (t TranscodeItems) Journalled(ctx context.Context) ([]TranscodeItem, error) {
	rows, err := librarydb.New(t.store.Read()).ListJournalledTranscodeItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: journalled transcode items: %w", err)
	}
	out := make([]TranscodeItem, len(rows))
	for i, row := range rows {
		out[i] = TranscodeItem{MediaObjectID: row.MediaObjectID, CategoryID: row.CategoryID, Kind: row.Kind, ReplaceStage: row.ReplaceStage, WorkPath: row.WorkPath, OriginalRecycleID: row.OriginalRecycleID.Int64}
	}
	return out, nil
}

// ResetFailed gives every failed item of a target another chance, once: the counters follow in the same transaction.
func (t TranscodeItems) ResetFailed(ctx context.Context, targetID int64) error {
	now := t.clock.Now().Unix()
	return t.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		counts, err := queries.CountFailedTranscodeItemsByCategory(ctx, targetID)
		if err != nil {
			return err
		}
		if err := queries.ResetFailedTranscodeItems(ctx, librarydb.ResetFailedTranscodeItemsParams{UpdatedAt: now, TargetID: targetID}); err != nil {
			return err
		}
		for _, count := range counts {
			if err := adjustState(ctx, queries, count.CategoryID, TranscodeFailed, -count.Total, now); err != nil {
				return err
			}
			if err := adjustState(ctx, queries, count.CategoryID, TranscodeNeeded, count.Total, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Counts answers the true number of items per state, counted from the rows; zero category means every library.
func (t TranscodeItems) Counts(ctx context.Context, categoryID int64) (map[TranscodeState]int64, error) {
	rows, err := librarydb.New(t.store.Read()).CountTranscodeItemsByState(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: count transcode items: %w", err)
	}
	counts := make(map[TranscodeState]int64, len(TranscodeStates))
	for _, state := range TranscodeStates {
		counts[state] = 0
	}
	for _, row := range rows {
		counts[TranscodeState(row.State)] = row.Total
	}
	return counts, nil
}

// ReconcileCounters corrects each scope's transcode counters to the rows; the counters drift because items can vanish with their media object.
func (t TranscodeItems) ReconcileCounters(ctx context.Context, scopes []ReconcileScope) ([]Correction, error) {
	var corrections []Correction
	for _, scope := range scopes {
		truth, err := t.Counts(ctx, scope.CategoryID)
		if err != nil {
			return nil, err
		}
		stored, err := NewCounters(t.store).Counters(ctx, scope.Scope)
		if err != nil {
			return nil, err
		}
		now := t.clock.Now().Unix()
		err = t.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			writer := librarydb.New(tx)
			for _, state := range TranscodeStates {
				metric, value := TranscodeMetric(state), truth[state]
				was, wasSet := stored[metric]
				if wasSet && was == value {
					continue
				}
				if err := writer.SetCounter(ctx, librarydb.SetCounterParams{Scope: scope.Scope, Metric: metric, Value: value, UpdatedAt: now}); err != nil {
					return err
				}
				corrections = append(corrections, Correction{Scope: scope.Scope, Metric: metric, Was: was, WasSet: wasSet, Now: value})
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("library: reconcile transcode counters %s: %w", scope.Scope, err)
		}
	}
	return corrections, nil
}

// TranscodeHealth is how one kind of conversion is doing; times are unix seconds, zero for none.
type TranscodeHealth struct {
	Kind                                                        string
	ConsecutiveFailures                                         int64
	Mode                                                        string
	ProbesLeft, BackoffStep, BackoffUntil, LastFailedID, RunSeq int64
	LastError                                                   string
}

// Health answers the health of a kind; a kind never saved is healthy.
func (t TranscodeItems) Health(ctx context.Context, kind string) (TranscodeHealth, error) {
	row, err := librarydb.New(t.store.Read()).GetTranscodeHealth(ctx, kind)
	if errors.Is(err, sql.ErrNoRows) {
		return TranscodeHealth{Kind: kind, Mode: "ok"}, nil
	}
	if err != nil {
		return TranscodeHealth{}, fmt.Errorf("library: transcode health: %w", err)
	}
	return TranscodeHealth{
		Kind: row.Kind, ConsecutiveFailures: row.ConsecutiveFailures, Mode: row.Mode, ProbesLeft: row.ProbesLeft, BackoffStep: row.BackoffStep,
		BackoffUntil: row.BackoffUntil.Int64, LastFailedID: row.LastFailedID, LastError: row.LastError, RunSeq: row.RunSeq,
	}, nil
}

// SaveHealth stores the health of a kind.
func (t TranscodeItems) SaveHealth(ctx context.Context, health TranscodeHealth) error {
	return t.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).UpsertTranscodeHealth(ctx, librarydb.UpsertTranscodeHealthParams{
			Kind: health.Kind, ConsecutiveFailures: health.ConsecutiveFailures, Mode: health.Mode, ProbesLeft: health.ProbesLeft,
			BackoffStep: health.BackoffStep, BackoffUntil: sql.NullInt64{Int64: health.BackoffUntil, Valid: health.BackoffUntil != 0},
			LastFailedID: health.LastFailedID, LastError: health.LastError, RunSeq: health.RunSeq,
		})
	})
}

// OriginalsKept is the bytes of originals still in the recycle bin after a conversion.
func (t TranscodeItems) OriginalsKept(ctx context.Context) (int64, error) {
	total, err := librarydb.New(t.store.Read()).SumTranscodeOriginals(ctx)
	if err != nil {
		return 0, fmt.Errorf("library: kept originals: %w", err)
	}
	return total, nil
}

// KeptOriginal is one converted file's original in the recycle bin.
type KeptOriginal struct {
	RecycleID     int64
	MediaObjectID int64
	SizeBytes     int64
	RecycledAt    int64
}

// OldestOriginals answers the oldest kept originals, for the size cap to drop.
func (t TranscodeItems) OldestOriginals(ctx context.Context, limit int64) ([]KeptOriginal, error) {
	rows, err := librarydb.New(t.store.Read()).ListOldestTranscodeOriginals(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("library: oldest kept originals: %w", err)
	}
	out := make([]KeptOriginal, len(rows))
	for i, row := range rows {
		out[i] = KeptOriginal{RecycleID: row.ID, MediaObjectID: row.MediaObjectID.Int64, SizeBytes: row.SizeBytes, RecycledAt: row.RecycledAt}
	}
	return out, nil
}
