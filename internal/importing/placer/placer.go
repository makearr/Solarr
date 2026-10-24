// Package placer is the placing half of an import that every media type shares: which root a work goes to, how a file
// gets there, the blocked-content gate, permissions and taking a failed unit back.
package placer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/placement"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Roots is the settings-side facts placing a file needs.
type Roots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
	CategoryRoots() []settings.CategoryRoot
}

// Affinity keeps a work on one root once placed (section 11.7).
type Affinity interface {
	PlacementAffinity(ctx context.Context, categoryID int64, affinityKey string) (int64, bool, error)
	SetPlacementAffinity(ctx context.Context, categoryID int64, affinityKey string, rootFolderID, updatedAt int64) error
}

// Blocked is the ADR-53 hash gate: bytes that failed before never enter the library.
type Blocked interface {
	FindByHash(ctx context.Context, oshash, quickHash string, now int64) ([]library.BlockedContent, error)
}

// Config is what a Placer needs; Blocked, TransferLadder, GroupID and Logger are optional.
type Config struct {
	FS             fsx.FS
	Roots          Roots
	Affinity       Affinity
	Identity       library.Identity
	Blocked        Blocked
	Clock          clock.Clock
	Logger         *slog.Logger
	TransferLadder func() []mover.PlaceMethod
	GroupID        func(name string) (int, error)
}

// Placer places the files of one unit for a media type that names them itself.
type Placer struct {
	config Config
	logger *slog.Logger
}

// New builds a Placer.
func New(config Config) Placer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Placer{config: config, logger: logger}
}

// Placed is one file put in a root, kept so a failed unit can be taken back.
type Placed struct {
	Source, Dest, Rel, Hash string
	Size                    int64
	Method                  mover.PlaceMethod
}

// Methods is the transfer ladder: the request's, else the configured one, else the default.
func (p Placer) Methods(ladder []string) []mover.PlaceMethod {
	methods := mover.DefaultLadder
	if p.config.TransferLadder != nil {
		if configured := p.config.TransferLadder(); len(configured) > 0 {
			methods = configured
		}
	}
	if len(ladder) > 0 {
		methods = make([]mover.PlaceMethod, len(ladder))
		for index, method := range ladder {
			methods[index] = mover.PlaceMethod(method)
		}
	}
	return methods
}

// FreeName keeps an upgrade from landing on the file it replaces when both are named alike.
func (p Placer) FreeName(rootPath, rel string) string {
	if _, err := p.config.FS.Stat(path.Join(rootPath, rel)); err != nil {
		return rel
	}
	extension := path.Ext(rel)
	return strings.TrimSuffix(rel, extension) + " [upgrade]" + extension
}

// File is one file to check against the blocked content.
type File struct {
	RelPath   string
	SizeBytes int64
}

// CheckBlocked hashes every file before any is placed and refuses the unit when one is blocked content (ADR-53).
func (p Placer) CheckBlocked(ctx context.Context, sourceRoot string, files []File) ([]string, error) {
	hashes := make([]string, len(files))
	for index, file := range files {
		hash, err := library.QuickHash(p.config.FS, path.Join(sourceRoot, file.RelPath), file.SizeBytes)
		if err != nil {
			return nil, err
		}
		hashes[index] = hash
		if p.config.Blocked == nil {
			continue
		}
		blocks, err := p.config.Blocked.FindByHash(ctx, "", hash, p.config.Clock.Now().Unix())
		if err != nil {
			return nil, err
		}
		if len(blocks) > 0 {
			return nil, fmt.Errorf("blocked content (%s): %s", blocks[0].Reason, file.RelPath)
		}
	}
	return hashes, nil
}

// ChooseRoot keeps a work where it already is: the root of a file it has, else its recorded affinity, else the
// placement strategy's choice. Files first, so a work moved to another root receives its next file there too.
func (p Placer) ChooseRoot(ctx context.Context, categoryID, workID int64, affinityKey string, bytes int64) (settings.RootFolder, error) {
	var existing *int64
	roots, err := p.config.Identity.WorkFileRoots(ctx, workID)
	if err != nil {
		return settings.RootFolder{}, fmt.Errorf("find existing work roots: %w", err)
	}
	if len(roots) > 0 {
		rootID := roots[0].RootFolderID
		existing = &rootID
	}
	if existing == nil && affinityKey != "" {
		if rootID, found, err := p.config.Affinity.PlacementAffinity(ctx, categoryID, affinityKey); err != nil {
			return settings.RootFolder{}, err
		} else if found {
			existing = &rootID
		}
	}
	root, decision, err := placement.Resolve(placement.Request{
		CategoryID: categoryID, SizeBytes: bytes, Strategy: placement.StrategyMostFreeSpace,
		AffinityEnabled: affinityKey != "", AffinityKey: affinityKey, ExistingAffinityRoot: existing,
	}, p.rootCandidates(categoryID))
	if err != nil {
		return settings.RootFolder{}, fmt.Errorf("resolve placement: %w", err)
	}
	if decision.Outcome != placement.OutcomePlaced {
		return settings.RootFolder{}, fmt.Errorf("no root folder can take it: %s", decision.Reason)
	}
	destRoot, ok := p.config.Roots.RootFolder(root.RootFolderID)
	if !ok {
		return settings.RootFolder{}, fmt.Errorf("root folder %d has no known path", root.RootFolderID)
	}
	if affinityKey == "" {
		return destRoot, nil
	}
	if err := p.config.Affinity.SetPlacementAffinity(ctx, categoryID, affinityKey, root.RootFolderID, p.config.Clock.Now().Unix()); err != nil {
		return settings.RootFolder{}, err
	}
	return destRoot, nil
}

func (p Placer) rootCandidates(categoryID int64) []placement.RootCandidate {
	acceptsNew := map[int64]bool{}
	var order []int64
	seen := map[int64]bool{}
	for _, attachment := range p.config.Roots.CategoryRoots() {
		if attachment.CategoryID != categoryID {
			continue
		}
		if !seen[attachment.RootFolderID] {
			seen[attachment.RootFolderID] = true
			order = append(order, attachment.RootFolderID)
		}
		if attachment.AcceptsNew {
			acceptsNew[attachment.RootFolderID] = true
		}
	}
	candidates := make([]placement.RootCandidate, 0, len(order))
	for _, id := range order {
		root, ok := p.config.Roots.RootFolder(id)
		if !ok {
			continue
		}
		var free, total int64
		if root.FreeBytes != nil {
			free = *root.FreeBytes
		}
		if root.TotalBytes != nil {
			total = *root.TotalBytes
		}
		candidates = append(candidates, placement.RootCandidate{
			RootFolderID: root.ID, FSID: root.FSID, Online: root.Online, Writable: root.Writable, ReadOnly: root.ReadOnly,
			AcceptsNew: acceptsNew[id], FreeBytes: free, TotalBytes: total, MinFreeBytes: root.MinFreeBytes,
		})
	}
	return candidates
}

// Finish applies the library's Set Permissions and Change File Date to a placed file; a failure is only logged.
// dated is the date the file's modification time becomes, zero for none.
func (p Placer) Finish(root settings.RootFolder, fileAbs string, fileManagement settings.FileManagement, dated time.Time) {
	if fileManagement.ChangeFileDate == settings.FileDateReleaseDate && !root.ReadOnly && root.Writable && !dated.IsZero() {
		if err := p.config.FS.Chtimes(fileAbs, dated); err != nil {
			p.logger.Warn("import: file date not changed", slog.String("path", fileAbs), slog.Any("error", err))
		}
	}
	if !fileManagement.SetPermissions {
		return
	}
	permissions := mover.Permissions{FolderMode: fileManagement.FolderMode(), FileMode: fileManagement.FileMode(), GID: -1}
	if group := fileManagement.ChownGroup; group != "" {
		if gid, err := strconv.Atoi(group); err == nil {
			permissions.GID = gid
		} else if p.config.GroupID != nil {
			if gid, err := p.config.GroupID(group); err == nil {
				permissions.GID = gid
			}
		}
	}
	if err := mover.ApplyPermissions(p.config.FS, root.Path, fileAbs, permissions); err != nil {
		p.logger.Warn("import: permissions not set", slog.String("path", fileAbs), slog.Any("error", err))
	}
}

// Undo takes back a failed unit's files: a moved file returns to its source, anything else is removed.
func (p Placer) Undo(placed []Placed) error {
	var failures []error
	for _, one := range placed {
		var err error
		if one.Method == mover.PlaceMove {
			_, err = mover.PlaceOne(p.config.FS, one.Dest, one.Source, one.Size, []mover.PlaceMethod{mover.PlaceMove})
		} else {
			err = p.config.FS.Remove(one.Dest)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("import: could not take back %q: %w", one.Dest, err))
		}
	}
	return errors.Join(failures...)
}

// Owner is a placed file that other files of its unit may be named after.
type Owner struct{ SourceRel, DestRel string }

// Extras brings along the unit's other files that are named after a placed file, its subtitles above all (Sonarr's and
// Radarr's rule): each keeps what follows the file's name (".en.srt") under the file's new name. A file already there is
// left, and one that cannot be placed is logged, never the import's failure.
func (p Placer) Extras(
	extensions []string, sourceRoot string, destRoot settings.RootFolder, others []inspect.File, owners []Owner, methods []mover.PlaceMethod,
) []library.UnitExtra {
	wanted := map[string]bool{}
	for _, extension := range extensions {
		wanted["."+strings.ToLower(extension)] = true
	}
	var extras []library.UnitExtra
	for _, other := range others {
		if !wanted[strings.ToLower(path.Ext(other.RelPath))] {
			continue
		}
		var owner *Owner
		var suffix string
		for index := range owners {
			rest, named := strings.CutPrefix(other.RelPath, strings.TrimSuffix(owners[index].SourceRel, path.Ext(owners[index].SourceRel)))
			// The file named most exactly owns it: "S01E01E02.en.srt" is not the subtitle of "S01E01".
			if named && strings.HasPrefix(rest, ".") && (owner == nil || len(rest) < len(suffix)) {
				owner, suffix = &owners[index], rest
			}
		}
		if owner == nil {
			continue
		}
		destRel := strings.TrimSuffix(owner.DestRel, path.Ext(owner.DestRel)) + suffix
		destAbs := path.Join(destRoot.Path, destRel)
		if _, err := p.config.FS.Stat(destAbs); err == nil {
			continue
		}
		if _, err := mover.PlaceOne(p.config.FS, path.Join(sourceRoot, other.RelPath), destAbs, other.SizeBytes, methods); err != nil {
			p.logger.Warn("import: could not bring an extra file along", slog.String("file", other.RelPath), slog.Any("error", err))
			continue
		}
		extras = append(extras, library.UnitExtra{RootFolderID: destRoot.ID, RelPath: destRel})
	}
	return extras
}
