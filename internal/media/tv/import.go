package tv

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/placement"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// PlacementRoots is the settings-side facts placing a file needs.
type PlacementRoots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
	CategoryRoots() []settings.CategoryRoot
}

// PlacementAffinity keeps a series on one root once placed (section 11.7).
type PlacementAffinity interface {
	PlacementAffinity(ctx context.Context, categoryID int64, affinityKey string) (int64, bool, error)
	SetPlacementAffinity(ctx context.Context, categoryID int64, affinityKey string, rootFolderID, updatedAt int64) error
}

// BlockedContent is the ADR-53 hash gate: bytes that failed before never enter the library.
type BlockedContent interface {
	FindByHash(ctx context.Context, oshash, quickHash string, now int64) ([]library.BlockedContent, error)
}

// AcquisitionSource finds the desired item a finished download was grabbed for.
type AcquisitionSource interface {
	Get(ctx context.Context, id int64) (library.Acquisition, error)
}

// ReasonPreview is the import job's own reason for a unit a preview run held for a person.
const ReasonPreview = "preview"

// ImporterConfig is what an Importer needs; Logger, TransferLadder, RenameOnImport and GroupID are optional.
type ImporterConfig struct {
	FS           fsx.FS
	Roots        PlacementRoots
	Affinity     PlacementAffinity
	Physical     library.Physical
	Series       library.Series
	Reader       library.SeriesReader
	Identity     library.Identity
	Desired      library.Desired
	Acquisitions AcquisitionSource
	Resolver     Resolver
	Categories   interface {
		Category(id int64) (settings.Category, bool)
	}
	BlockedContent BlockedContent
	Clock          clock.Clock
	Logger         *slog.Logger
	TransferLadder func() []mover.PlaceMethod
	RenameOnImport func() bool
	GroupID        func(name string) (int, error)
}

// Importer is the TV module's batch importer: it works out which episodes a folder of video files holds, names and
// places each file below the series folder, and records which episodes it covers. What it cannot place waits for a
// person in the Review queue.
type Importer struct {
	config ImporterConfig
	logger *slog.Logger
}

// NewImporter builds the importer.
func NewImporter(config ImporterConfig) Importer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Importer{config: config, logger: logger}
}

var _ batch.Importer = Importer{}

// ImportBatch implements batch.Importer.
func (i Importer) ImportBatch(ctx context.Context, request batch.Request) (batch.Result, error) {
	result := batch.Result{
		Decisions:  make([]library.UnitDecision, len(request.Units)),
		Placements: make([]*library.Placement, len(request.Units)),
	}
	var seeded int64
	var upgrade bool
	if request.AcquisitionID != nil {
		seeded, upgrade = i.seedSeries(ctx, *request.AcquisitionID)
	}
	for index, unit := range request.Units {
		decision := library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitQueue, Reason: "no episode of a known series was found in it"}
		workID, candidates := seeded, []model.Identification(nil)
		if workID == 0 {
			folder := path.Base(request.SourcePath)
			if unit.Directory != "" {
				folder = path.Base(unit.Directory)
			}
			workID, candidates = i.config.Resolver.Resolve(ctx, request.CategoryID, folder, firstMedia(unit))
		}
		if workID <= 0 {
			result.Decisions[index] = decision
			result.Decisions[index].CandidatesJSON = library.CandidatesJSON(candidates)
			continue
		}
		if request.Preview {
			decision.Reason = ReasonPreview
			decision.WorkID = &workID
			result.Decisions[index] = decision
			continue
		}
		placed, err := i.placeUnit(ctx, request, unit, workID, upgrade && workID == seeded)
		switch {
		case errors.Is(err, errNothingToPlace):
			decision.WorkID = &workID
			result.Decisions[index] = decision
		case err != nil:
			i.logger.Warn("tv import: could not place unit", slog.String("unit", unit.Directory), slog.Any("error", err))
			decision.Decision, decision.Reason = library.UnitFailed, err.Error()
			result.Decisions[index] = decision
		default:
			decision.Decision, decision.Reason, decision.Confidence = library.UnitCommit, "", 1
			decision.WorkID = &workID
			result.Decisions[index] = decision
			result.Placements[index] = placed
		}
	}
	return result, nil
}

func firstMedia(unit inspect.Unit) string {
	if len(unit.Media) > 0 {
		return unit.Media[0].RelPath
	}
	return ""
}

// seedSeries is the series a download was grabbed for: its desired item's work; upgrade says the item was below its
// cutoff, so the download is meant to replace files it already has.
func (i Importer) seedSeries(ctx context.Context, acquisitionID int64) (workID int64, upgrade bool) {
	acquisition, err := i.config.Acquisitions.Get(ctx, acquisitionID)
	if err != nil || acquisition.DesiredItemID == nil {
		return 0, false
	}
	item, err := i.config.Desired.Get(ctx, *acquisition.DesiredItemID)
	if err != nil {
		return 0, false
	}
	return item.WorkID, item.State == ItemStateUpgradable
}

var errNothingToPlace = errors.New("tv: no file of the unit covers an episode that is wanted")

// fileEpisodes is one video file and the episodes it covers.
type fileEpisodes struct {
	media inspect.File
	info  parsetv.Info
	parts []library.ResolvedPart
}

func (i Importer) placeUnit(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	now := i.config.Clock.Now()
	detail, err := i.config.Reader.Get(ctx, request.CategoryID, workID, now)
	if err != nil {
		return nil, fmt.Errorf("series %d: %w", workID, err)
	}
	present := map[string]bool{}
	for _, episode := range detail.Episodes {
		if episode.Present {
			present[MediaRef(episode.Season, episode.Number)] = true
		}
	}

	var files []fileEpisodes
	var totalBytes int64
	for _, media := range unit.Media {
		info, ok := parsetv.ParsePath(media.RelPath, now)
		if !ok {
			continue
		}
		parts, _, err := i.config.Series.ResolveParts(ctx, workID, partKeys(info))
		if err != nil {
			return nil, err
		}
		// An upgrade brings a second file for an episode; the cutoff check keeps the better one once both are measured.
		fresh := slices.DeleteFunc(slices.Clone(parts), func(part library.ResolvedPart) bool { return present[part.MediaRef] && !upgrade })
		if len(fresh) == 0 {
			continue
		}
		files = append(files, fileEpisodes{media: media, info: info, parts: fresh})
		totalBytes += media.SizeBytes
	}
	if len(files) == 0 {
		return nil, errNothingToPlace
	}

	hashes, err := i.checkBlocked(ctx, request.SourcePath, files)
	if err != nil {
		return nil, err
	}
	names, fileManagement := i.namingFor(request.CategoryID)
	destRoot, err := i.chooseRoot(ctx, request.CategoryID, workID, detail.Summary.Title, totalBytes)
	if err != nil {
		return nil, err
	}
	methods := i.methods(request.Ladder)

	placed := make([]placedEpisode, 0, len(files))
	undo := func(cause error) (*library.Placement, error) { return nil, errors.Join(cause, i.undo(placed)) }
	for index, file := range files {
		naming := episodeNaming(detail, file)
		destRel, _, err := names.RenderEpisode(naming, file.media.RelPath)
		if err != nil {
			return undo(fmt.Errorf("name %q: %w", file.media.RelPath, err))
		}
		sourceAbs := path.Join(request.SourcePath, file.media.RelPath)
		if upgrade {
			destRel = i.freeName(destRoot.Path, destRel)
		}
		destAbs := path.Join(destRoot.Path, destRel)
		method, err := mover.PlaceOne(i.config.FS, sourceAbs, destAbs, file.media.SizeBytes, methods)
		if err != nil {
			return undo(fmt.Errorf("place %q: %w", file.media.RelPath, err))
		}
		placed = append(placed, placedEpisode{source: sourceAbs, dest: destAbs, size: file.media.SizeBytes, method: method, rel: destRel, hash: hashes[index], file: file})
		if method != mover.PlaceSymlink {
			i.finish(destRoot, destAbs, fileManagement, file.parts)
		}
	}

	physical := make([]library.PhysicalFile, len(placed))
	for index, p := range placed {
		physical[index] = library.PhysicalFile{RelPath: p.rel, SizeBytes: p.size, QuickHash: p.hash, FirstSeenAt: now}
	}
	registered, err := i.config.Physical.RegisterBatch(ctx, destRoot.ID, physical)
	if err != nil {
		return undo(fmt.Errorf("register placed files: %w", err))
	}
	links := make([]library.SeriesFileLink, len(placed))
	for index, p := range placed {
		keys := make([]library.PartKey, len(p.file.parts))
		for k, part := range p.file.parts {
			keys[k] = library.PartKey{MediaRef: part.MediaRef}
		}
		links[index] = library.SeriesFileLink{MediaObjectID: registered[index].MediaObjectID, Parts: keys}
	}
	if _, err := i.config.Series.AttachFiles(ctx, now, workID, destRoot.ID, links); err != nil {
		return undo(err)
	}

	placement := &library.Placement{WorkID: workID}
	for index, p := range placed {
		placement.MediaObjectIDs = append(placement.MediaObjectIDs, registered[index].MediaObjectID)
		placement.LibraryPathIDs = append(placement.LibraryPathIDs, registered[index].LibraryPathID)
		placement.TransferMethods = append(placement.TransferMethods, string(p.method))
	}
	return placement, nil
}

// freeName keeps an upgrade from landing on the file it replaces when both are named alike.
func (i Importer) freeName(rootPath, rel string) string {
	if _, err := i.config.FS.Stat(path.Join(rootPath, rel)); err != nil {
		return rel
	}
	extension := path.Ext(rel)
	return strings.TrimSuffix(rel, extension) + " [upgrade]" + extension
}

type placedEpisode struct {
	source, dest, rel, hash string
	size                    int64
	method                  mover.PlaceMethod
	file                    fileEpisodes
}

// undo takes back a failed unit's files: a moved file returns to its source, anything else is removed.
func (i Importer) undo(placed []placedEpisode) error {
	var failures []error
	for _, p := range placed {
		var err error
		if p.method == mover.PlaceMove {
			_, err = mover.PlaceOne(i.config.FS, p.dest, p.source, p.size, []mover.PlaceMethod{mover.PlaceMove})
		} else {
			err = i.config.FS.Remove(p.dest)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("tv: could not take back %q: %w", p.dest, err))
		}
	}
	return errors.Join(failures...)
}

// checkBlocked hashes every file before any is placed and refuses the unit when one is blocked content (ADR-53).
func (i Importer) checkBlocked(ctx context.Context, sourceRoot string, files []fileEpisodes) ([]string, error) {
	hashes := make([]string, len(files))
	for index, file := range files {
		hash, err := library.QuickHash(i.config.FS, path.Join(sourceRoot, file.media.RelPath), file.media.SizeBytes)
		if err != nil {
			return nil, err
		}
		hashes[index] = hash
		if i.config.BlockedContent == nil {
			continue
		}
		blocks, err := i.config.BlockedContent.FindByHash(ctx, "", hash, i.config.Clock.Now().Unix())
		if err != nil {
			return nil, err
		}
		if len(blocks) > 0 {
			return nil, fmt.Errorf("blocked content (%s): %s", blocks[0].Reason, file.media.RelPath)
		}
	}
	return hashes, nil
}

func (i Importer) namingFor(categoryID int64) (Naming, settings.FileManagement) {
	names := NamingFor(nil)
	fileManagement := settings.DefaultFileManagement
	if category, found := i.config.Categories.Category(categoryID); found {
		names = NamingFor(category.Naming)
		fileManagement = category.FileManagement
	}
	if i.config.RenameOnImport != nil && !i.config.RenameOnImport() {
		names.RenameFiles = false
	}
	return names, fileManagement
}

func (i Importer) methods(ladder []string) []mover.PlaceMethod {
	methods := mover.DefaultLadder
	if i.config.TransferLadder != nil {
		if configured := i.config.TransferLadder(); len(configured) > 0 {
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

// chooseRoot keeps a series where it already is: its recorded affinity, else the root of a file it already has, else
// the placement strategy's choice among the library's roots.
func (i Importer) chooseRoot(ctx context.Context, categoryID, workID int64, title string, bytes int64) (settings.RootFolder, error) {
	key := "series:" + parsetv.CleanTitle(title)
	var existing *int64
	if rootID, found, err := i.config.Affinity.PlacementAffinity(ctx, categoryID, key); err != nil {
		return settings.RootFolder{}, err
	} else if found {
		existing = &rootID
	}
	if existing == nil {
		if roots, err := i.config.Identity.WorkFileRoots(ctx, workID); err == nil && len(roots) > 0 {
			rootID := roots[0].RootFolderID
			existing = &rootID
		}
	}
	root, decision, err := placement.Resolve(placement.Request{
		CategoryID: categoryID, SizeBytes: bytes, Strategy: placement.StrategyMostFreeSpace,
		AffinityEnabled: key != "series:", AffinityKey: key, ExistingAffinityRoot: existing,
	}, i.rootCandidates(categoryID))
	if err != nil {
		return settings.RootFolder{}, fmt.Errorf("resolve placement: %w", err)
	}
	if decision.Outcome != placement.OutcomePlaced {
		return settings.RootFolder{}, fmt.Errorf("no root folder can take it: %s", decision.Reason)
	}
	destRoot, ok := i.config.Roots.RootFolder(root.RootFolderID)
	if !ok {
		return settings.RootFolder{}, fmt.Errorf("root folder %d has no known path", root.RootFolderID)
	}
	if err := i.config.Affinity.SetPlacementAffinity(ctx, categoryID, key, root.RootFolderID, i.config.Clock.Now().Unix()); err != nil {
		return settings.RootFolder{}, err
	}
	return destRoot, nil
}

func (i Importer) rootCandidates(categoryID int64) []placement.RootCandidate {
	acceptsNew := map[int64]bool{}
	var order []int64
	seen := map[int64]bool{}
	for _, attachment := range i.config.Roots.CategoryRoots() {
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
		root, ok := i.config.Roots.RootFolder(id)
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

// finish applies the library's Set Permissions to a placed file; a failure is not worth failing an import over.
func (i Importer) finish(root settings.RootFolder, fileAbs string, fileManagement settings.FileManagement, parts []library.ResolvedPart) {
	rootPath := root.Path
	i.changeFileDate(root, fileAbs, fileManagement, parts)
	if !fileManagement.SetPermissions {
		return
	}
	permissions := mover.Permissions{FolderMode: fileManagement.FolderMode(), FileMode: fileManagement.FileMode(), GID: -1}
	if group := fileManagement.ChownGroup; group != "" {
		if gid, err := strconv.Atoi(group); err == nil {
			permissions.GID = gid
		} else if i.config.GroupID != nil {
			if gid, err := i.config.GroupID(group); err == nil {
				permissions.GID = gid
			}
		}
	}
	if err := mover.ApplyPermissions(i.config.FS, rootPath, fileAbs, permissions); err != nil {
		i.logger.Warn("tv import: permissions not set", slog.String("path", fileAbs), slog.Any("error", err))
	}
}

// episodeNaming gathers everything a placed file's name can draw on.
func episodeNaming(detail library.SeriesDetailRow, file fileEpisodes) EpisodeNaming {
	summary := detail.Summary
	seriesType := SeriesType(summary.Fields["series_type"])
	if !seriesType.Valid() {
		seriesType = SeriesStandard
	}
	naming := EpisodeNaming{
		SeriesType: seriesType, SeriesTitle: summary.Title, SeriesYear: int(summary.Year),
		IDs:          idsOf(detail.IDs),
		QualityTitle: qualityTitle(file.info.Claims), Proper: file.info.Claims.Version > 1, Version: file.info.Claims.Version,
		VideoCodec: strings.ToUpper(file.info.Claims.VideoCodec), DynamicRange: file.info.Claims.HDR,
		AudioCodec: strings.ToUpper(file.info.Claims.AudioCodec), AudioChannels: file.info.Claims.AudioChannels,
		ReleaseGroup: file.info.ReleaseGroup, OriginalTitle: file.info.ReleaseTitle,
		OriginalFilename: strings.TrimSuffix(path.Base(file.media.RelPath), path.Ext(file.media.RelPath)),
	}
	parts := slices.Clone(file.parts)
	slices.SortFunc(parts, func(a, b library.ResolvedPart) int { return cmp.Compare(a.MediaRef, b.MediaRef) })
	for _, part := range parts {
		season, number := splitRef(part.MediaRef)
		naming.Season = season
		naming.Episodes = append(naming.Episodes, EpisodeNumbering{Number: number, Title: part.Title})
		if naming.AirDate == "" {
			naming.AirDate = part.Date
		}
	}
	if file.info.AirDate != "" {
		naming.SeriesType = SeriesDaily
		naming.AirDate = file.info.AirDate
	}
	return naming
}

func splitRef(ref string) (season, episode int) {
	head, tail, _ := strings.Cut(ref, ":")
	season, _ = strconv.Atoi(strings.TrimPrefix(head, "s"))
	episode, _ = strconv.Atoi(strings.TrimPrefix(tail, "e"))
	return season, episode
}

func idsOf(ids map[string]string) IDs {
	var out IDs
	out.TVDB, _ = strconv.ParseInt(ids[ProviderTVDB], 10, 64)
	out.TVmaze, _ = strconv.ParseInt(ids[ProviderTVmaze], 10, 64)
	out.TMDB, _ = strconv.ParseInt(ids[ProviderTMDB], 10, 64)
	out.IMDb = ids[ProviderIMDb]
	return out
}

// qualityTitle is Sonarr's "WEBDL-1080p": the source and the resolution a name claims.
func qualityTitle(claims parsetv.Claims) string {
	source := map[string]string{"webdl": "WEBDL", "webrip": "WEBRip", "bluray": "Bluray", "bdrip": "Bluray", "brrip": "Bluray", "hdtv": "HDTV", "dvd": "DVD", "sdtv": "SDTV", "pdtv": "SDTV", "dsr": "SDTV", "tvrip": "SDTV", "rawhd": "Raw-HD"}[claims.Source]
	if claims.Remux {
		source = "Bluray"
	}
	switch {
	case source == "":
		return ""
	case claims.Resolution == 0:
		return source
	case claims.Remux:
		return source + "-" + strconv.Itoa(claims.Resolution) + "p Remux"
	}
	return source + "-" + strconv.Itoa(claims.Resolution) + "p"
}
