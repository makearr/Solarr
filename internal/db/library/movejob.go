package library

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Move job states (move_job.state).
const (
	MoveJobPlanned = "PLANNED"
	MoveJobRunning = "RUNNING"
	MoveJobPaused  = "PAUSED"
	MoveJobDone    = "DONE"
	MoveJobFailed  = "FAILED"
)

// Move item states (move_item.state) -- ADR-51's own Apply algorithm, one
// state per step: PENDING before anything runs, COPIED once the bytes are
// written (same-filesystem rename(2) skips straight to VERIFIED, since it
// has no separate copy step), VERIFIED once size and hash match, DONE once
// library_path is repointed and the source is unlinked.
const (
	MoveItemPending  = "PENDING"
	MoveItemCopied   = "COPIED"
	MoveItemVerified = "VERIFIED"
	MoveItemDone     = "DONE"
	MoveItemFailed   = "FAILED"
)

// MoveJobs is ADR-51's own persistence: one row per move operation, one row
// per file within it. internal/moving/mover.Plan is pure and in-memory (ADR-23,
// the same separation internal/importing/inspect keeps); this is where a caller
// persists a plan once accepted, so a restart has something to resume.
type MoveJobs struct{ store *store.Store }

// NewMoveJobs builds it over an open library store.
func NewMoveJobs(opened *store.Store) MoveJobs { return MoveJobs{store: opened} }

// Job is one move_job row.
type Job struct {
	ID           int64
	Reason       string
	SourceRootID *int64
	State        string
	FilesTotal   int64
	FilesDone    int64
	BytesTotal   int64
	BytesDone    int64
	PlanJSON     string
	CreatedAt    int64
	FinishedAt   *int64
	Error        string
	// CategoryID is the library a rename ran for; nil for a move that is not one library's.
	CategoryID *int64
}

// NewMoveJob is what Create needs to start one job.
type NewMoveJob struct {
	Reason       string
	SourceRootID *int64
	FilesTotal   int64
	BytesTotal   int64
	PlanJSON     string
	CreatedAt    int64
	CategoryID   *int64
}

// Item is one move_item row.
type Item struct {
	MoveJobID     int64
	MediaObjectID int64
	AffinityKey   string
	DestRootID    int64
	DestRelPath   string
	Bytes         int64
	State         string
	Error         string
	// KeepSource copies the file to its destination as a further path instead of moving it.
	KeepSource bool
}

// NewMoveItem is what AddItems needs for one file a plan approved.
type NewMoveItem struct {
	MediaObjectID int64
	AffinityKey   string
	DestRootID    int64
	DestRelPath   string
	Bytes         int64
	// Companions move with the file: its subtitles, nfo, pictures, which are no media objects of their own.
	Companions []MoveCompanion
	// KeepSource copies instead of moving: the file stays and the copy is a further path of it.
	KeepSource bool
}

// MoveCompanion is one file that moves with an item, within the item's root.
type MoveCompanion struct {
	SourceRelPath, DestRelPath string
}

// Create starts one job in MoveJobPlanned, with no items yet.
func (m MoveJobs) Create(ctx context.Context, params NewMoveJob) (int64, error) {
	var id int64
	err := m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		row, err := librarydb.New(tx).CreateMoveJob(ctx, librarydb.CreateMoveJobParams{
			Reason: params.Reason, SourceRootID: nullInt64(params.SourceRootID),
			FilesTotal: params.FilesTotal, BytesTotal: params.BytesTotal,
			PlanJson: params.PlanJSON, CreatedAt: params.CreatedAt, CategoryID: nullInt64(params.CategoryID),
		})
		if err != nil {
			return err
		}
		id = row.ID
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("library: create move job: %w", err)
	}
	return id, nil
}

// AddItems records every file one accepted plan approved, in one
// transaction (Invariant 2) -- each starts MoveItemPending, Apply's own
// job to work through them.
func (m MoveJobs) AddItems(ctx context.Context, jobID int64, items []NewMoveItem) error {
	if len(items) == 0 {
		return nil
	}
	for chunk := range slices.Chunk(items, moveItemChunk) {
		err := m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, item := range chunk {
				if err := queries.InsertMoveItem(ctx, librarydb.InsertMoveItemParams{
					MoveJobID: jobID, MediaObjectID: item.MediaObjectID, AffinityKey: item.AffinityKey,
					DestRootID: item.DestRootID, DestRelPath: item.DestRelPath, Bytes: item.Bytes,
					State: MoveItemPending, KeepSource: boolToInt(item.KeepSource),
				}); err != nil {
					return fmt.Errorf("insert item for media object %d: %w", item.MediaObjectID, err)
				}
				for _, companion := range item.Companions {
					if err := queries.InsertMoveItemCompanion(ctx, librarydb.InsertMoveItemCompanionParams{
						MoveJobID: jobID, MediaObjectID: item.MediaObjectID,
						SourceRelPath: companion.SourceRelPath, DestRelPath: companion.DestRelPath,
					}); err != nil {
						return fmt.Errorf("insert companion %q of media object %d: %w", companion.SourceRelPath, item.MediaObjectID, err)
					}
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: add items to move job %d: %w", jobID, err)
		}
	}
	return nil
}

// moveItemChunk is how many items one write adds, so a whole root's rename stays within the bulk-write budget.
const moveItemChunk = 200

// Companions answers the files that move with one item.
func (m MoveJobs) Companions(ctx context.Context, jobID, mediaObjectID int64) ([]MoveCompanion, error) {
	rows, err := librarydb.New(m.store.Read()).ListMoveItemCompanions(ctx, librarydb.ListMoveItemCompanionsParams{
		MoveJobID: jobID, MediaObjectID: mediaObjectID,
	})
	if err != nil {
		return nil, fmt.Errorf("library: companions of item %d in move job %d: %w", mediaObjectID, jobID, err)
	}
	companions := make([]MoveCompanion, len(rows))
	for index, row := range rows {
		companions[index] = MoveCompanion{SourceRelPath: row.SourceRelPath, DestRelPath: row.DestRelPath}
	}
	return companions, nil
}

// SetRunning transitions a job from PLANNED to RUNNING -- once, at the start
// of the first Apply attempt; a resumed Apply on an already-RUNNING job
// leaves this alone.
func (m MoveJobs) SetRunning(ctx context.Context, jobID int64) error {
	err := m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetMoveJobRunning(ctx, jobID)
	})
	if err != nil {
		return fmt.Errorf("library: set move job %d running: %w", jobID, err)
	}
	return nil
}

// NextPendingItem answers the next item Apply should work on -- ordered by
// media_object_id, so a resumed job continues deterministically rather than
// racing its own restart over which item comes next. sql.ErrNoRows means
// every item has reached a terminal state (DONE or FAILED).
func (m MoveJobs) NextPendingItem(ctx context.Context, jobID int64) (Item, error) {
	row, err := librarydb.New(m.store.Read()).NextPendingMoveItem(ctx, jobID)
	if err != nil {
		return Item{}, err
	}
	return itemFromRow(row), nil
}

// SetItemState records one item's own progress and recomputes the job's
// running totals -- the same drift-and-repair shape
// RecomputeImportSessionCounts already uses, rather than an incremented
// counter that can drift from the rows it is meant to summarise.
func (m MoveJobs) SetItemState(ctx context.Context, jobID, mediaObjectID int64, state, errMsg string) error {
	err := m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.SetMoveItemState(ctx, librarydb.SetMoveItemStateParams{
			MoveJobID: jobID, MediaObjectID: mediaObjectID, State: state, Error: nullString(errMsg),
		}); err != nil {
			return err
		}
		return queries.RecomputeMoveJobProgress(ctx, jobID)
	})
	if err != nil {
		return fmt.Errorf("library: set move item %d/%d state: %w", jobID, mediaObjectID, err)
	}
	return nil
}

// Finish transitions a job to a terminal state (MoveJobDone/MoveJobFailed)
// and records when and why.
func (m MoveJobs) Finish(ctx context.Context, jobID int64, state string, finishedAt int64, errMsg string) error {
	err := m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).FinishMoveJob(ctx, librarydb.FinishMoveJobParams{
			ID: jobID, State: state, FinishedAt: nullInt64(&finishedAt), Error: nullString(errMsg),
		})
	})
	if err != nil {
		return fmt.Errorf("library: finish move job %d: %w", jobID, err)
	}
	return nil
}

// Get answers one job by id.
func (m MoveJobs) Get(ctx context.Context, id int64) (Job, error) {
	row, err := librarydb.New(m.store.Read()).GetMoveJob(ctx, id)
	if err != nil {
		return Job{}, fmt.Errorf("library: get move job %d: %w", id, err)
	}
	return jobFromRow(row), nil
}

// List returns every job, newest first, optionally filtered to one state --
// a plain list, not cursor-paginated: move job counts never approach
// library scale, the same reasoning ImportSessions.List's own comment gives.
func (m MoveJobs) List(ctx context.Context, filterState string) ([]Job, error) {
	rows, err := librarydb.New(m.store.Read()).ListMoveJobs(ctx, librarydb.ListMoveJobsParams{
		FilterByState: boolToInt(filterState != ""), State: filterState,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list move jobs: %w", err)
	}
	jobs := make([]Job, len(rows))
	for i, row := range rows {
		jobs[i] = jobFromRow(row)
	}
	return jobs, nil
}

// ListItems returns every item one job holds, in media_object_id order.
func (m MoveJobs) ListItems(ctx context.Context, jobID int64) ([]Item, error) {
	rows, err := librarydb.New(m.store.Read()).ListMoveItems(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("library: list items for move job %d: %w", jobID, err)
	}
	items := make([]Item, len(rows))
	for i, row := range rows {
		items[i] = itemFromRow(row)
	}
	return items, nil
}

func jobFromRow(row librarydb.MoveJob) Job {
	return Job{
		ID: row.ID, Reason: row.Reason, SourceRootID: int64Ptr(row.SourceRootID),
		State: row.State, FilesTotal: row.FilesTotal, FilesDone: row.FilesDone,
		BytesTotal: row.BytesTotal, BytesDone: row.BytesDone, PlanJSON: row.PlanJson,
		CreatedAt: row.CreatedAt, FinishedAt: int64Ptr(row.FinishedAt), Error: row.Error.String,
		CategoryID: int64Ptr(row.CategoryID),
	}
}

func itemFromRow(row librarydb.MoveItem) Item {
	return Item{
		MoveJobID: row.MoveJobID, MediaObjectID: row.MediaObjectID, AffinityKey: row.AffinityKey,
		DestRootID: row.DestRootID, DestRelPath: row.DestRelPath, Bytes: row.Bytes,
		State: row.State, Error: row.Error.String, KeepSource: row.KeepSource == 1,
	}
}
