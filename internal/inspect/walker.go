package inspect

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/makearr/MediaNexus/internal/fsx"
)

// WalkState is one resumable Walk's persisted progress -- the same role
// scan.State's Cursor plays for a library sweep, reused directly rather than
// reinvented: the last cluster (one grouped directory's worth of units) this
// walk fully committed, relative to the root. Empty means start from the
// beginning.
type WalkState struct {
	Cursor string
}

// WalkOptions tune one resumable walk. Every field is optional, the same as
// Options.
type WalkOptions struct {
	Tags                TagReader
	SegmentPlausible    SegmentPlausibility
	MediaSizeFloorBytes int64
}

// WalkResult is what one resumable walk did.
type WalkResult struct {
	State WalkState
	// Completed is false when the walk was cancelled part-way -- what
	// distinguishes "resume here" from "the cursor is now valid".
	Completed         bool
	UnitsEmitted      int
	ContainerSegments []string
}

// Walker walks one root incrementally, emitting each fully-grouped cluster's
// units as soon as they are known rather than building the whole session in
// memory -- ADR-48's own text: "it streams units as it finds them". Run (a
// single in-memory pass) stays exactly as it is for what most callers actually
// point it at, a single downloaded release; Walker is what a whole-library
// bulk-adoption entry point needs, the same 500k-file problem section 10.4
// already solves for a library scan, generalised to inspection's own grouping.
//
// Resuming a walk re-reads the whole tree (inspection has no cheap mtime
// watermark of its own the way a scan does, section 10.4) but skips re-emitting
// -- and so re-probing and re-persisting, the genuinely expensive half a real
// caller's IDENTIFY step pays for -- any cluster already committed before an
// interruption. Watermark-based skip-reading is a natural follow-up once a real
// caller exists to measure whether it is worth the added complexity; not
// attempted here.
type Walker struct{}

// NewWalker builds one. Stateless -- every Walk call is independent, the same
// as Sweeper.
func NewWalker() Walker { return Walker{} }

// clusterFiles is what one directory contributes to its own decision, or bubbles
// up to its parent instead when its own name matches the disc-subfolder pattern.
// Only media bubbles: mergeDiscSubfolders (Run's own implementation) never
// touches a disc-subfolder child's own non-media files either, so a CD1/folder.jpg
// is dropped, not attached to the merged unit, in both Run and Walker alike --
// an existing, minor, unchanged gap, not something this pass introduces or fixes.
type clusterFiles struct {
	media    []File
	children []string
}

// walkerRun is one Walk call's mutable state, kept off Walker so a Walker
// stays reusable and safe to share, the same separation sweep keeps from
// Sweeper.
type walkerRun struct {
	fsys fsx.FS
	root string
	opts WalkOptions
	emit func(context.Context, []Unit) error
	agg  *containerAggregator

	// resumeFrom is the cursor an interrupted walk left behind. reached flips
	// true the instant this run's own (deterministic, sorted) traversal passes
	// it, gating every emission before that point -- post-order has no cheap
	// path-comparison shortcut the way a pre-order sweep's cursor does, so this
	// walk tracks its position in the sequence directly instead.
	resumeFrom string
	reached    bool

	cursor  string
	emitted int
}

// Walk runs one resumable inspection pass over root, calling emit for every
// cluster of units it groups. emit is where a caller persists and identifies,
// so returning an error from it stops the walk with the progress made so far
// intact -- state.Cursor names the last cluster committed.
func (Walker) Walk(
	ctx context.Context, fsys fsx.FS, root string, state WalkState, opts WalkOptions,
	emit func(context.Context, []Unit) error,
) (WalkResult, error) {
	if opts.MediaSizeFloorBytes <= 0 {
		opts.MediaSizeFloorBytes = DefaultMediaSizeFloorBytes
	}
	w := &walkerRun{
		fsys: fsys, root: root, opts: opts, emit: emit,
		agg:        newContainerAggregator(),
		resumeFrom: state.Cursor, reached: state.Cursor == "",
	}

	_, err := w.visit(ctx, "")
	result := WalkResult{State: WalkState{Cursor: w.cursor}, UnitsEmitted: w.emitted}
	if err != nil {
		return result, err
	}
	if !w.reached {
		// The cursor names a directory this run's traversal never reached --
		// deleted, renamed, or the tree changed shape between runs. Silently
		// treating "never found it" as "already past it" would skip the entire
		// remaining walk, the one failure mode worse than redoing work.
		return result, fmt.Errorf("inspect: resume cursor %q was not found in this walk", state.Cursor)
	}

	result.Completed = true
	result.ContainerSegments = w.agg.Result(opts.SegmentPlausible)
	return result, nil
}

// visit walks one directory post-order (every subdirectory fully processed
// before this directory decides anything), matching mergeDiscSubfolders' own
// rule that a parent's merge decision needs its children's files already known.
func (w *walkerRun) visit(ctx context.Context, relative string) (clusterFiles, error) {
	if err := ctx.Err(); err != nil {
		return clusterFiles{}, err
	}

	absolute := w.root
	if relative != "" {
		absolute = path.Join(w.root, relative)
	}
	entries, err := w.fsys.ReadDir(absolute)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return clusterFiles{}, nil
		}
		return clusterFiles{}, fmt.Errorf("inspect: read %q: %w", absolute, err)
	}
	slices.SortFunc(entries, func(a, b fsx.DirEntry) int { return strings.Compare(a.Name, b.Name) })

	var ownMedia, ownOther []File
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		childRelative := path.Join(relative, entry.Name)
		info, err := w.fsys.Stat(path.Join(absolute, entry.Name))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return clusterFiles{}, fmt.Errorf("inspect: stat %q: %w", path.Join(absolute, entry.Name), err)
		}
		file := File{
			RelPath: childRelative, SizeBytes: info.Size, ModTime: info.ModTime,
			Class: Classify(childRelative, info.Size, w.opts.MediaSizeFloorBytes),
		}
		if file.Class == ClassMedia {
			ownMedia = append(ownMedia, file)
		} else {
			ownOther = append(ownOther, file)
		}
	}

	var bubbled clusterFiles
	for _, entry := range entries {
		if !entry.IsDir {
			continue
		}
		child, err := w.visit(ctx, path.Join(relative, entry.Name))
		if err != nil {
			return clusterFiles{}, err
		}
		if len(child.media) > 0 {
			bubbled.media = append(bubbled.media, child.media...)
			bubbled.children = append(bubbled.children, child.children...)
		}
	}

	allMedia := append(append([]File(nil), ownMedia...), bubbled.media...)

	if relative != "" && discSubfolderRe.MatchString(path.Base(relative)) {
		// Defer to the parent, exactly mergeDiscSubfolders' own rule -- a
		// directory shaped like "CD1" never becomes a unit on its own.
		return clusterFiles{media: allMedia, children: []string{relative}}, nil
	}

	if len(allMedia) == 0 {
		return clusterFiles{}, nil
	}

	// Captured before updating reached: resumeFrom names the *last* cluster an
	// interrupted run already committed, so that one cluster itself must be
	// skipped here too, not re-emitted -- only clusters strictly after it in
	// this run's own deterministic sequence are new work. Checked only here,
	// among real cluster-root candidates, since resumeFrom can only ever equal
	// a directory that previously was one (w.cursor is set nowhere else).
	shouldEmit := w.reached
	if relative == w.resumeFrom {
		w.reached = true
	}

	var extra *Signal
	if len(bubbled.children) > 0 {
		extra = &Signal{Kind: SignalDiscSubfolder, Detail: strings.Join(bubbled.children, ", ")}
	}
	units := splitByEvidence(relative, allMedia, w.opts.Tags, extra)
	for i := range units {
		units[i].Other = ownOther
	}

	if shouldEmit {
		if err := w.emit(ctx, units); err != nil {
			return clusterFiles{}, err
		}
		w.cursor = relative
		w.emitted += len(units)
	}
	for _, u := range units {
		w.agg.Add(u)
	}
	return clusterFiles{}, nil
}
