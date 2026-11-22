package inspect

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/iogate"
)

// WalkState is one resumable Walk's progress: the last cluster fully committed, relative to the root (like scan.State's Cursor); empty means start over.
type WalkState struct {
	Cursor string
}

// WalkOptions tune one resumable walk. Every field is optional, the same as
// Options.
type WalkOptions struct {
	Tags TagGrouper
	// TagsFor reads the tags of one cluster's media just before it is split; an error falls back to Tags.
	TagsFor             func(ctx context.Context, relPaths []string) (TagGrouper, error)
	MediaSizeFloorBytes int64
	// Books counts ebook files as media, for a book library's import.
	Books bool
	// Only, when set, is the files (relative to the root) the walk may see; everything else is as if absent.
	Only map[string]bool
}

// WalkResult is what one resumable walk did.
type WalkResult struct {
	State WalkState
	// Completed is false when the walk was cancelled part-way -- what
	// distinguishes "resume here" from "the cursor is now valid".
	Completed    bool
	UnitsEmitted int
}

// Walker walks one root incrementally and emits each grouped cluster as soon as it is known (ADR-48 "streams units"), for whole-library adoption; Run stays the in-memory pass for a single release.
// Resuming re-reads the tree (inspection has no mtime watermark, section 10.4) but skips re-emitting, re-probing and re-persisting clusters already committed; watermark skipping awaits a real caller to measure.
type Walker struct{}

// NewWalker builds one. Stateless -- every Walk call is independent, the same
// as Sweeper.
func NewWalker() Walker { return Walker{} }

// EstimateUnits counts the units a Walk would emit without probing or persisting (one readdir and one stat per file), using the same WALK/GROUP logic; it undercounts where only tag evidence would split a directory, an accepted inaccuracy for a progress ETA (todo 4.21), and is deliberately a second walk, opt-in.
func EstimateUnits(ctx context.Context, fsys fsx.FS, root string) (int, error) {
	var total int
	_, err := NewWalker().Walk(ctx, fsys, root, WalkState{}, WalkOptions{},
		func(_ context.Context, units []Unit) error {
			total += len(units)
			return nil
		})
	return total, err
}

// clusterFiles is what one directory contributes to its own decision, or bubbles
// up to its parent instead when its own name matches the disc-subfolder pattern.
// Only media and the discs' own sidecars bubble; a disc subfolder is never a unit of its own.
type clusterFiles struct {
	media    []File
	children []string
	// other are the sidecars and artwork of the disc subfolders, which follow the unit the parent makes of them.
	other []File
}

// walkerRun is one Walk call's mutable state, kept off Walker so a Walker
// stays reusable and safe to share, the same separation sweep keeps from
// Sweeper.
type walkerRun struct {
	fsys fsx.FS
	root string
	opts WalkOptions
	emit func(context.Context, []Unit) error

	// resumeFrom is the cursor an interrupted walk left; reached flips true once this run's deterministic sorted traversal passes it, gating earlier emissions, since post-order has no cheap path comparison.
	resumeFrom string
	reached    bool

	cursor  string
	emitted int
}

// Walk runs one resumable pass over root, calling emit per cluster; an emit error stops the walk with progress kept (state.Cursor names the last cluster committed).
func (Walker) Walk(
	ctx context.Context, fsys fsx.FS, root string, state WalkState, opts WalkOptions,
	emit func(context.Context, []Unit) error,
) (WalkResult, error) {
	if opts.MediaSizeFloorBytes <= 0 {
		opts.MediaSizeFloorBytes = DefaultMediaSizeFloorBytes
	}
	w := &walkerRun{
		fsys: fsys, root: root, opts: opts, emit: emit,
		resumeFrom: state.Cursor, reached: state.Cursor == "",
	}

	_, err := w.visit(ctx, "")
	result := WalkResult{State: WalkState{Cursor: w.cursor}, UnitsEmitted: w.emitted}
	if err != nil {
		return result, err
	}
	if !w.reached {
		// A cursor naming a directory this traversal never reached (deleted, renamed, tree changed) must not read as "already past it", which would skip the entire remaining walk, worse than redoing work.
		return result, fmt.Errorf("inspect: resume cursor %q was not found in this walk", state.Cursor)
	}

	result.Completed = true
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
	ownMedia, ownOther, dirs, err := readDirectory(ctx, w.fsys, absolute, relative, w.opts)
	if err != nil {
		return clusterFiles{}, err
	}

	var bubbled clusterFiles
	var discFolders []string
	for _, entry := range dirs {
		if isDiscStructureFolder(entry.Name) {
			discMedia, discOther, err := w.readWholeTree(ctx, path.Join(relative, entry.Name))
			if err != nil {
				return clusterFiles{}, err
			}
			bubbled.media = append(bubbled.media, discMedia...)
			ownOther = append(ownOther, discOther...)
			discFolders = append(discFolders, entry.Name)
			continue
		}
		if isSubtitleFolder(entry.Name) {
			ownOther = append(ownOther, w.subtitleFiles(path.Join(relative, entry.Name))...)
		}
		child, err := w.visit(ctx, path.Join(relative, entry.Name))
		if err != nil {
			return clusterFiles{}, err
		}
		if len(child.media) > 0 {
			bubbled.media = append(bubbled.media, child.media...)
			bubbled.children = append(bubbled.children, child.children...)
			ownOther = append(ownOther, child.other...)
		}
	}

	allMedia := append(append([]File(nil), ownMedia...), bubbled.media...)

	if relative != "" && discSubfolderRe.MatchString(path.Base(relative)) {
		// Defer to the parent, exactly mergeDiscSubfolders' own rule -- a
		// directory shaped like "CD1" never becomes a unit on its own.
		return clusterFiles{media: allMedia, children: []string{relative}, other: ownOther}, nil
	}

	if len(allMedia) == 0 {
		return clusterFiles{}, nil
	}

	// Captured before updating reached: resumeFrom names the last committed cluster, so that cluster is skipped too and only strictly later ones are new; checked only among cluster-root candidates since w.cursor is set nowhere else.
	shouldEmit := w.reached
	if relative == w.resumeFrom {
		w.reached = true
	}

	var extra *Signal
	if len(bubbled.children) > 0 {
		extra = &Signal{Kind: SignalDiscSubfolder, Detail: strings.Join(bubbled.children, ", ")}
	}
	var units []Unit
	if err := iogate.OnDisk(ctx, "", []string{absolute}, func() error {
		tags := w.opts.Tags
		if w.opts.TagsFor != nil && len(allMedia) > 1 {
			relPaths := make([]string, len(allMedia))
			for i, file := range allMedia {
				relPaths[i] = file.RelPath
			}
			if read, err := w.opts.TagsFor(ctx, relPaths); err == nil {
				tags = read
			}
		}
		if len(discFolders) > 0 {
			units = []Unit{{
				Directory: relative, Media: allMedia,
				Evidence: Evidence{Signals: []Signal{{Kind: SignalDiscStructure, Detail: strings.Join(discFolders, ", ")}}},
			}}
			return nil
		}
		units = splitByEvidence(relative, allMedia, tags, extra)
		return nil
	}); err != nil {
		return clusterFiles{}, err
	}
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
	return clusterFiles{}, nil
}

// readDirectory lists one directory the way every inspection reads a download: sorted, a symlink never followed, a file
// that vanished meanwhile skipped, a missing directory empty, the recycle folders left out, and each file classified.
// Subdirectories are returned for the caller to descend into.
func readDirectory(
	ctx context.Context, fsys fsx.FS, absolute, relative string, opts WalkOptions,
) (media, other []File, dirs []fsx.DirEntry, err error) {
	var entries []fsx.DirEntry
	err = iogate.OnDisk(ctx, "", []string{absolute}, func() (readErr error) {
		entries, readErr = fsys.ReadDir(absolute)
		return readErr
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("inspect: read %q: %w", absolute, err)
	}
	slices.SortFunc(entries, func(a, b fsx.DirEntry) int { return strings.Compare(a.Name, b.Name) })

	for _, entry := range entries {
		if entry.IsDir {
			if entry.Name != RecycleFolder && entry.Name != recycleBinFolder {
				dirs = append(dirs, entry)
			}
			continue
		}
		childRelative := path.Join(relative, entry.Name)
		if opts.Only != nil && !opts.Only[childRelative] {
			continue
		}
		// Never a symlink: what it points at is not the download's to import (ADR-11).
		info, err := fsys.Lstat(path.Join(absolute, entry.Name))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, nil, nil, fmt.Errorf("inspect: stat %q: %w", path.Join(absolute, entry.Name), err)
		}
		if info.Mode&fs.ModeSymlink != 0 {
			continue
		}
		file := File{
			RelPath: childRelative, SizeBytes: info.Size, ModTime: info.ModTime,
			Class: classify(childRelative, info.Size, opts.MediaSizeFloorBytes, opts.Books),
		}
		if file.Class == ClassMedia {
			media = append(media, file)
		} else {
			other = append(other, file)
		}
	}
	return media, other, dirs, nil
}

// isDiscStructureFolder names the folders a Blu-ray or DVD keeps its streams in; the disc is one unit, never its clips.
func isDiscStructureFolder(name string) bool {
	switch strings.ToUpper(name) {
	case "BDMV", "VIDEO_TS", "AUDIO_TS", "CERTIFICATE":
		return true
	}
	return false
}

// readWholeTree lists every file below a folder without turning any subfolder into a unit of its own.
func (w *walkerRun) readWholeTree(ctx context.Context, relative string) (media, other []File, err error) {
	m, o, dirs, err := readDirectory(ctx, w.fsys, path.Join(w.root, relative), relative, w.opts)
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range dirs {
		childMedia, childOther, err := w.readWholeTree(ctx, path.Join(relative, entry.Name))
		if err != nil {
			return nil, nil, err
		}
		m, o = append(m, childMedia...), append(o, childOther...)
	}
	return m, o, nil
}

// isSubtitleFolder is the folder scene releases keep their subtitles in, beside the video.
func isSubtitleFolder(name string) bool {
	switch strings.ToLower(name) {
	case "subs", "sub", "subtitles", "subtitle", "vobsub", "idx":
		return true
	}
	return false
}

// subtitleFolderDepth is how many folders below a subtitle folder are read: scene packs keep "Subs/<episode name>/*.srt".
const subtitleFolderDepth = 2

// subtitleFiles lists the subtitles inside a subtitle folder and the folders under it, so they follow the unit's videos.
func (w *walkerRun) subtitleFiles(relative string) []File {
	return w.subtitlesBelow(relative, subtitleFolderDepth)
}

func (w *walkerRun) subtitlesBelow(relative string, depth int) []File {
	entries, err := w.fsys.ReadDir(path.Join(w.root, relative))
	if err != nil {
		return nil
	}
	slices.SortFunc(entries, func(a, b fsx.DirEntry) int { return strings.Compare(a.Name, b.Name) })
	var files []File
	for _, entry := range entries {
		childRelative := path.Join(relative, entry.Name)
		if entry.IsDir {
			if depth > 0 && entry.Name != RecycleFolder && entry.Name != recycleBinFolder {
				files = append(files, w.subtitlesBelow(childRelative, depth-1)...)
			}
			continue
		}
		if w.opts.Only != nil && !w.opts.Only[childRelative] {
			continue
		}
		info, err := w.fsys.Lstat(path.Join(w.root, childRelative))
		if err != nil || info.Mode&fs.ModeSymlink != 0 {
			continue
		}
		if class := classify(childRelative, info.Size, w.opts.MediaSizeFloorBytes, w.opts.Books); class == ClassSubtitle {
			files = append(files, File{RelPath: childRelative, SizeBytes: info.Size, ModTime: info.ModTime, Class: class})
		}
	}
	return files
}
