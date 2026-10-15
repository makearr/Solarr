package tv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// SharedFiles puts the file of one broadcast listed under two series into the second series' folder as well, as a
// link to the same bytes where the filesystem allows (Stage 8 T23, ADR-11, Invariant 92).
type SharedFiles struct {
	FS         fsx.FS
	Crossovers library.Crossovers
	Reader     library.SeriesReader
	Links      library.ObjectLinks
	Series     library.Series
	Categories interface {
		Category(id int64) (settings.Category, bool)
	}
	Roots interface {
		RootFolder(id int64) (settings.RootFolder, bool)
	}
	// RootsOf answers the roots a library is on, for a series with no folder yet.
	RootsOf func(categoryID int64) []settings.RootFolder
	Clock   clock.Clock
	Logger  *slog.Logger
}

// seriesHome is a series' own folder on one root.
type seriesHome struct {
	rootID int64
	folder string
}

// PlaceAll places every shared file a series lacks in its own folder, answering how many it placed; one that cannot
// be placed is logged and left for the next pass.
func (s SharedFiles) PlaceAll(ctx context.Context) (int, error) {
	pairs, err := s.Crossovers.Pairs(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	byWork := map[int64][]library.SameContentPair{}
	var works []int64
	for _, pair := range pairs {
		if _, seen := byWork[pair.WorkID]; !seen {
			works = append(works, pair.WorkID)
		}
		byWork[pair.WorkID] = append(byWork[pair.WorkID], pair)
	}
	placed := 0
	for _, workID := range works {
		if err := ctx.Err(); err != nil {
			return placed, err
		}
		made, err := s.placeForSeries(ctx, workID, byWork[workID])
		placed += made
		if err != nil {
			s.Logger.Warn("tv: shared files not placed", slog.Int64("work_id", workID), slog.Any("error", err))
		}
	}
	return placed, nil
}

// placeForSeries places, for each linked episode of one series that has no file in the series' own folder, the file
// the other series holds.
func (s SharedFiles) placeForSeries(ctx context.Context, workID int64, pairs []library.SameContentPair) (int, error) {
	category, found := s.Categories.Category(pairs[0].CategoryID)
	if !found {
		return 0, fmt.Errorf("library %d unknown", pairs[0].CategoryID)
	}
	paths, err := s.Reader.PartPaths(ctx, workID, ProviderTVDB+":episode")
	if err != nil {
		return 0, err
	}
	linked := map[string]bool{}
	for _, pair := range pairs {
		linked[pair.MediaRef] = true
	}
	homes := seriesHomes(paths, linked)
	placed := 0
	for _, pair := range pairs {
		var held []library.PartPath
		own := false
		for _, file := range paths {
			if file.MediaRef != pair.MediaRef {
				continue
			}
			held = append(held, file)
			own = own || slices.Contains(homes, seriesHome{file.RootFolderID, seriesFolderOf(file.RelPath)})
		}
		if own || len(held) == 0 {
			continue
		}
		if err := s.placeOne(ctx, category, workID, pair, held, homes); err != nil {
			s.Logger.Warn("tv: shared episode not placed", slog.Int64("work_id", workID), slog.String("episode", pair.MediaRef), slog.Any("error", err))
			continue
		}
		placed++
	}
	return placed, nil
}

// placeOne puts one held file at the episode's name in the series' folder, reflink then hardlink, else the library's
// choice of a copy or a symlink, and records it as a further path of the same file.
func (s SharedFiles) placeOne(ctx context.Context, category settings.Category, workID int64, pair library.SameContentPair, held []library.PartPath, homes []seriesHome) error {
	source, sourceAbs, size, found := s.readable(held)
	if !found {
		return errors.New("no file of the other series can be read")
	}
	now := s.Clock.Now()
	detail, err := s.Reader.Get(ctx, category.ID, workID, now)
	if err != nil {
		return err
	}
	info, _ := parsetv.ParsePath(source.RelPath, now)
	names := NamingFor(category.Naming)
	episode := episodeNaming(detail, fileEpisodes{
		media: inspect.File{RelPath: source.RelPath, SizeBytes: size}, info: info,
		parts: []library.ResolvedPart{{PartID: pair.PartID, MediaRef: pair.MediaRef, Title: pair.Title, Date: pair.Date}},
	})
	rendered, _, err := names.RenderEpisode(episode, source.RelPath)
	if err != nil {
		return err
	}
	root, rel, err := s.destination(category, names, episode, rendered, source, homes)
	if err != nil {
		return err
	}
	destAbs := path.Join(root.Path, rel)
	if _, err := s.FS.Stat(destAbs); err == nil {
		return fmt.Errorf("%s is already there and is left alone", rel)
	}
	fallback := mover.PlaceCopy
	if category.FileManagement.SharedFileFallback == settings.SharedFileSymlink {
		fallback = mover.PlaceSymlink
	}
	method, err := mover.PlaceOne(s.FS, sourceAbs, destAbs, size, []mover.PlaceMethod{mover.PlaceReflink, mover.PlaceHardlink, fallback})
	if err != nil {
		return err
	}
	if err := s.Links.AddLinkedPath(ctx, source.MediaObjectID, root.ID, rel, linkKindOf(method)); err != nil {
		return errors.Join(err, s.FS.Remove(destAbs))
	}
	_, err = s.Series.AttachFiles(ctx, now, workID, root.ID, []library.SeriesFileLink{{
		MediaObjectID: source.MediaObjectID, Parts: []library.PartKey{{MediaRef: pair.MediaRef}}, KeepMonitoring: true,
	}})
	return err
}

// readable is the first held file whose root is online and whose file is there.
func (s SharedFiles) readable(held []library.PartPath) (library.PartPath, string, int64, bool) {
	for _, file := range held {
		root, found := s.Roots.RootFolder(file.RootFolderID)
		if !found || !root.Online {
			continue
		}
		abs := path.Join(root.Path, file.RelPath)
		if info, err := s.FS.Stat(abs); err == nil {
			return file, abs, info.Size, true
		}
	}
	return library.PartPath{}, "", 0, false
}

// destination is the series' own folder with the episode's season folder and name, or, for a series with no folder
// yet, its rendered folder on the source's root when the library is on it, else on its first writable root.
func (s SharedFiles) destination(
	category settings.Category, names Naming, episode EpisodeNaming, rendered string, source library.PartPath, homes []seriesHome,
) (settings.RootFolder, string, error) {
	if len(homes) > 0 {
		root, found := s.Roots.RootFolder(homes[0].rootID)
		if !found || !writableRoot(root) {
			return settings.RootFolder{}, "", fmt.Errorf("the series' root folder %d is offline, not writable or read-only", homes[0].rootID)
		}
		folder, err := names.RenderSeriesFolder(episode)
		if err != nil {
			return settings.RootFolder{}, "", err
		}
		return root, path.Join(homes[0].folder, strings.TrimPrefix(rendered, folder+"/")), nil
	}
	roots := s.RootsOf(category.ID)
	slices.SortStableFunc(roots, func(a, b settings.RootFolder) int {
		return boolOrder(a.ID != source.RootFolderID) - boolOrder(b.ID != source.RootFolderID)
	})
	for _, root := range roots {
		if writableRoot(root) {
			return root, rendered, nil
		}
	}
	return settings.RootFolder{}, "", errors.New("the library has no writable root folder")
}

func boolOrder(b bool) int {
	if b {
		return 1
	}
	return 0
}

// seriesHomes are the folders a series' own files are in: those of its episodes not shared with another series.
func seriesHomes(paths []library.PartPath, linked map[string]bool) []seriesHome {
	var homes []seriesHome
	for _, file := range paths {
		home := seriesHome{file.RootFolderID, seriesFolderOf(file.RelPath)}
		if linked[file.MediaRef] || home.folder == "." || slices.Contains(homes, home) {
			continue
		}
		homes = append(homes, home)
	}
	return homes
}

// seriesFolderOf is the folder above an episode file's season folder, or the file's own folder when it has none.
func seriesFolderOf(relPath string) string {
	directory := path.Dir(relPath)
	if seasonFolder.MatchString(path.Base(directory)) {
		return path.Dir(directory)
	}
	return directory
}

func linkKindOf(method mover.PlaceMethod) string {
	switch method {
	case mover.PlaceReflink:
		return library.LinkReflink
	case mover.PlaceHardlink:
		return library.LinkHardlink
	case mover.PlaceSymlink:
		return library.LinkSymlink
	}
	return library.LinkPrimary
}

// SharedFilesJobType is the registered type of the pass placing shared broadcasts into both series.
const SharedFilesJobType = jobs.Type("tv.shared_files")

// SharedFilesDefinition declares the pass: file work, never in front of anything a person waits on.
var SharedFilesDefinition = jobs.Definition{
	Name: "Placing episodes shared by two series",
	Type: SharedFilesJobType, Pool: jobs.PoolMove, Mutating: true, MaxAttempts: 2,
}

// SharedFilesJob runs SharedFiles.PlaceAll.
type SharedFilesJob struct {
	Placer SharedFiles
}

// Run implements jobs.Handler.
func (j SharedFilesJob) Run(ctx context.Context, _ jobs.Job) error {
	placed, err := j.Placer.PlaceAll(ctx)
	if placed > 0 {
		j.Placer.Logger.Info("tv: shared episodes placed", slog.Int("files", placed))
	}
	return err
}

// EnqueueSharedFiles submits the pass; one already waiting covers the new request.
func EnqueueSharedFiles(ctx context.Context, queue *jobs.Queue) error {
	_, err := queue.Enqueue(ctx, jobs.Request{
		Type: SharedFilesJobType, ActionKey: string(SharedFilesJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
	return err
}
