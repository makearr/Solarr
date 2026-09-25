// Package scan walks library roots and reports what has changed, cheapest layer
// first (section 10.4). At ADR-22's 500k files a full rescan is not an answer.
package scan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// DefaultBatchSize is how many files one emitted batch carries, matching ADR-3's
// bulk chunk so a commit stays inside the transaction budget (Invariant 2).
const DefaultBatchSize = 200

// State is a sweep's persisted progress. A scan across 500k files is a job with
// progress, not a function call: a restart continues from the last committed batch
// (section 10.4, ADR-3).
type State struct {
	// SweptThrough is the watermark: the wall-clock time the last completed sweep
	// **started**. A directory whose mtime is older than this had no entries added,
	// removed or renamed since, so its files are not re-read.
	//
	// The start, not the end, and not the newest mtime seen. A directory written
	// while the sweep was running -- after the walk had already passed it -- would
	// otherwise sit behind the watermark and never be read again.
	SweptThrough time.Time

	// Cursor is the last directory whose files were committed, relative to the
	// root. Empty means start from the beginning.
	//
	// Comparing it takes comparePaths, not string order: the walk visits a
	// directory's whole subtree before its next sibling, so "a" then "a/c" then
	// "a-b" is the order, and "a-b" < "a/c" as plain strings.
	Cursor string
}

// Found is one file the sweep saw. Size and ModTime come from the directory walk,
// which is the cheap layer -- deciding whether the bytes changed is stat
// verification's job, one layer down.
type Found struct {
	RelPath   string
	SizeBytes int64
	ModTime   time.Time
}

// Batch is one directory's changed files, sized so a commit is bounded. A directory
// with more files than the batch size yields several batches, all naming it.
type Batch struct {
	// Directory is relative to the root, "" for the root itself.
	Directory string
	Files     []Found
}

// Result is what one sweep did.
type Result struct {
	// State is what to persist. On a cancelled sweep it resumes where this stopped;
	// on a completed one the watermark has moved.
	State State
	// Completed is false when the sweep was cancelled part-way, which is what
	// distinguishes "resume here" from "the watermark is now valid".
	Completed bool

	DirectoriesVisited int
	DirectoriesRead    int
	FilesFound         int
	// EmptyDirectories are the directories below the root the walk found with nothing in them,
	// capped at maxEmptyDirectories; a library's "Delete empty folders" acts on them.
	EmptyDirectories []string
}

const maxEmptyDirectories = 10000

// Options tune one sweep.
type Options struct {
	// BatchSize defaults to DefaultBatchSize.
	BatchSize int
	// Full ignores the watermark and reads every directory. It is the answer to
	// "the last sweep was interrupted in a way I do not trust", and to a first scan.
	Full bool
	// Include decides whether a file is worth reporting at all. A nil Include takes
	// everything, which is what a caller with no media-type opinion wants.
	Include func(relPath string) bool
}

// Sweeper walks one root folder.
type Sweeper struct {
	fsys  fsx.FS
	clock clock.Clock
}

// NewSweeper builds one over a filesystem and the injected clock (ADR-17).
func NewSweeper(fsys fsx.FS, cl clock.Clock) Sweeper { return Sweeper{fsys: fsys, clock: cl} }

// Sweep walks root and calls emit for each batch of files in a directory that has
// changed since state.SweptThrough. emit is where the caller commits, so returning
// an error from it stops the sweep with the progress made so far intact.
//
// A directory's mtime moves when its own entries change, not when a grandchild
// does, so the whole tree is still traversed -- only reading is skipped, which is
// the expensive half.
func (s Sweeper) Sweep(
	ctx context.Context,
	root string,
	state State,
	options Options,
	emit func(context.Context, Batch) error,
) (Result, error) {
	if options.BatchSize <= 0 {
		options.BatchSize = DefaultBatchSize
	}
	if !path.IsAbs(root) {
		return Result{}, fmt.Errorf("scan: root %q must be absolute", root)
	}

	// Taken before the walk, and it is what a completed sweep stores: see State.
	startedAt := s.clock.Now()

	walk := &sweep{
		sweeper: s, root: root, options: options,
		watermark: state.SweptThrough, resumeFrom: state.Cursor,
		emit: emit,
	}
	if options.Full {
		walk.watermark = time.Time{}
	}

	err := walk.directory(ctx, "")
	result := Result{
		State:              State{SweptThrough: state.SweptThrough, Cursor: walk.cursor},
		DirectoriesVisited: walk.visited,
		DirectoriesRead:    walk.read,
		FilesFound:         walk.files,
		EmptyDirectories:   walk.empty,
	}
	if err != nil {
		return result, err
	}

	// A completed sweep has no unfinished business, so the cursor clears and the
	// watermark moves to when this one began.
	result.Completed = true
	result.State = State{SweptThrough: startedAt}
	return result, nil
}

// sweep is one run's mutable state, kept off Sweeper so a Sweeper is reusable and
// safe to share.
type sweep struct {
	sweeper Sweeper
	root    string
	options Options
	emit    func(context.Context, Batch) error

	// watermark is what this sweep compares directory mtimes against.
	watermark time.Time
	// resumeFrom is the cursor an interrupted sweep left behind, and never moves.
	// Kept separate from cursor so progress made now can never be mistaken for
	// work already done.
	resumeFrom string
	// cursor is the last directory committed by this run.
	cursor string

	visited int
	read    int
	files   int
	empty   []string
}

func (w *sweep) directory(ctx context.Context, relative string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	absolute := w.root
	if relative != "" {
		absolute = path.Join(w.root, relative)
	}

	info, err := w.sweeper.fsys.Stat(absolute)
	if err != nil {
		// A directory that vanished mid-sweep is not a failed sweep. Section 10.4
		// is explicit that absence is reported, never acted on as deletion here.
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("scan: stat %q: %w", absolute, err)
	}
	w.visited++

	entries, err := w.sweeper.fsys.ReadDir(absolute)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("scan: read %q: %w", absolute, err)
	}
	// Lexical order is what makes Cursor enough to resume: the walk has to visit
	// directories in the same order every time.
	slices.SortFunc(entries, func(a, b fsx.DirEntry) int { return strings.Compare(a.Name, b.Name) })
	if len(entries) == 0 && relative != "" && len(w.empty) < maxEmptyDirectories {
		w.empty = append(w.empty, relative)
	}

	if err := w.filesIn(ctx, relative, absolute, info, entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir {
			continue
		}
		// Recycled files have left the library; walking them would bring them back.
		if relative == "" && (entry.Name == library.RecycleBinDirectory || entry.Name == library.LegacyRecycleBinDirectory) {
			continue
		}
		if err := w.directory(ctx, path.Join(relative, entry.Name)); err != nil {
			return err
		}
	}
	return nil
}

// filesIn emits this directory's files, unless the watermark or the resume cursor
// says they were already dealt with.
func (w *sweep) filesIn(
	ctx context.Context, relative, absolute string, info fsx.FileInfo, entries []fsx.DirEntry,
) error {
	// Already committed by the interrupted run this is resuming.
	if w.resumeFrom != "" && comparePaths(relative, w.resumeFrom) <= 0 {
		return nil
	}
	// The workhorse: a directory whose mtime predates the last sweep's start
	// gained, lost and renamed nothing since, so there is nothing here to read.
	// Not-after rather than before: a directory touched in the same instant the
	// last sweep started is re-read, because over-reading is recoverable and
	// missing a change is not.
	if !w.options.Full && info.ModTime.Before(w.watermark) {
		return nil
	}
	w.read++

	batch := Batch{Directory: relative}
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		childRelative := path.Join(relative, entry.Name)
		if w.options.Include != nil && !w.options.Include(childRelative) {
			continue
		}
		child, err := w.sweeper.fsys.Stat(path.Join(absolute, entry.Name))
		if err != nil {
			// Vanished between the read and the stat, which is routine on a live
			// library and not a reason to fail the sweep.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("scan: stat %q: %w", path.Join(absolute, entry.Name), err)
		}
		batch.Files = append(batch.Files, Found{
			RelPath: childRelative, SizeBytes: child.Size, ModTime: child.ModTime,
		})
		if len(batch.Files) == w.options.BatchSize {
			if err := w.commit(ctx, batch); err != nil {
				return err
			}
			batch.Files = nil
		}
	}
	// A directory whose files were all filtered out still counts as swept: the
	// cursor has to advance past it or a resume would read it again forever.
	if len(batch.Files) == 0 {
		w.cursor = relative
		return nil
	}
	return w.commit(ctx, batch)
}

// ReadDirectory lists the files directly inside one directory under root, the
// way a sweep would, but alone: no watermark, no cursor, no subdirectories. A
// directory that no longer exists is an empty batch, not an error.
func (s Sweeper) ReadDirectory(root, relative string, include func(relPath string) bool) (Batch, error) {
	absolute := path.Join(root, relative)
	entries, err := s.fsys.ReadDir(absolute)
	if errors.Is(err, fs.ErrNotExist) {
		return Batch{Directory: relative}, nil
	}
	if err != nil {
		return Batch{}, fmt.Errorf("scan: read %q: %w", absolute, err)
	}

	batch := Batch{Directory: relative}
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		childRelative := path.Join(relative, entry.Name)
		if include != nil && !include(childRelative) {
			continue
		}
		child, err := s.fsys.Stat(path.Join(absolute, entry.Name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Batch{}, fmt.Errorf("scan: stat %q: %w", path.Join(absolute, entry.Name), err)
		}
		batch.Files = append(batch.Files, Found{RelPath: childRelative, SizeBytes: child.Size, ModTime: child.ModTime})
	}
	return batch, nil
}

// comparePaths orders two relative paths the way the walk visits them: segment by
// segment, so a directory sorts before everything beneath it and before any sibling
// that follows it. Plain string comparison does not do this -- "a-b" < "a/c" because
// '-' is below '/' -- which would skip a sibling directory on resume.
func comparePaths(left, right string) int {
	leftSegments := splitPath(left)
	rightSegments := splitPath(right)
	for index := range min(len(leftSegments), len(rightSegments)) {
		if order := strings.Compare(leftSegments[index], rightSegments[index]); order != 0 {
			return order
		}
	}
	return cmp.Compare(len(leftSegments), len(rightSegments))
}

func splitPath(relative string) []string {
	if relative == "" {
		return nil
	}
	return strings.Split(relative, "/")
}

func (w *sweep) commit(ctx context.Context, batch Batch) error {
	if err := w.emit(ctx, batch); err != nil {
		return err
	}
	w.files += len(batch.Files)
	// Only after the caller has committed: the cursor means "everything up to
	// here is durable", so advancing it first would skip a batch on restart.
	w.cursor = batch.Directory
	return nil
}
