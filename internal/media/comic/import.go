package comic

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsecomic"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/importing/rules"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/mover"
)

// GuessFloor is the least confidence a held folder's guessed series is placed at by "Commit anyway".
const GuessFloor = 0.9

// ImporterConfig is what an Importer needs; Logger, RenameOnImport, ExtraFiles, AfterPlacing and Exclusions are optional.
type ImporterConfig struct {
	works.ImportDeps
	Resolver Resolver
}

// Importer is the comic module's batch importer: it works out which series a download holds and which issues, volumes or
// chapters each file is, names and places the files below the series' folder, and records them against their parts.
type Importer struct {
	works.Importer
	placing
}

type placing struct {
	config ImporterConfig
}

// NewImporter builds the importer.
func NewImporter(config ImporterConfig) Importer {
	units := placing{config: config}
	return Importer{works.NewImporter(works.ImporterConfig{
		MediaType: MediaType, Noun: "series", GuessFloor: GuessFloor, Units: units,
		Acquisitions: config.Acquisitions, Desired: config.Desired, Logger: config.Logger, AfterPlacing: config.AfterPlacing,
	}), units}
}

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
)

// comicFiles are the unit's comic files, in the order their names sort as numbers.
func comicFiles(unit inspect.Unit) []inspect.File {
	var files []inspect.File
	for _, file := range unit.Media {
		if Includes(file.RelPath) {
			files = append(files, file)
		}
	}
	slices.SortStableFunc(files, func(a, b inspect.File) int { return naturalCompare(a.RelPath, b.RelPath) })
	return files
}

// naturalCompare orders names as a person numbers them: "2" before "10".
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := leadingDigits(a), leadingDigits(b)
		if da != "" && db != "" {
			na, _ := strconv.ParseInt(da, 10, 64)
			nb, _ := strconv.ParseInt(db, 10, 64)
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return strings.Compare(strings.ToLower(a[:1]), strings.ToLower(b[:1]))
		}
		a, b = a[1:], b[1:]
	}
	return strings.Compare(a, b)
}

func leadingDigits(text string) string {
	end := 0
	for end < len(text) && end < 18 && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	return text[:end]
}

// downloadClaims are what the download's own folder name says, the weakest evidence of a file.
func downloadClaims(request batch.Request) []parsecomic.Claim {
	if request.SourcePath == "" {
		return nil
	}
	return []parsecomic.Claim{parsecomic.Parse(path.Base(request.SourcePath))}
}

// Resolve implements works.UnitPlacer: the series from the first file's embedded ids, name and folders, else from the
// download's own name.
func (p placing) Resolve(ctx context.Context, request batch.Request, unit inspect.Unit) (int64, []model.Identification) {
	files := comicFiles(unit)
	if len(files) == 0 {
		return 0, nil
	}
	facts, _ := Probe(ctx, p.config.FS, path.Join(request.SourcePath, files[0].RelPath))
	ev := evidenceOfPath(files[0].RelPath, facts)
	if titleKey(ev.Series) == "" || ev.Series == "." || ev.Series == "/" {
		if extra := EvidenceOf(nil, downloadClaims(request)...); extra.Series != "" {
			ev.Series = extra.Series
			if ev.Year == 0 {
				ev.Year = extra.Year
			}
		}
	}
	return p.config.Resolver.Resolve(ctx, request.CategoryID, ev)
}

// ResolveByName implements works.UnitPlacer: the series from the first file's name and folders, without opening the archive.
func (p placing) ResolveByName(ctx context.Context, request batch.Request, unit inspect.Unit) (int64, []model.Identification) {
	files := comicFiles(unit)
	if len(files) == 0 {
		return 0, nil
	}
	ev := evidenceOfPath(files[0].RelPath, Facts{})
	if titleKey(ev.Series) == "" || ev.Series == "." || ev.Series == "/" {
		extra := EvidenceOf(nil, downloadClaims(request)...)
		ev.Series = extra.Series
	}
	return p.config.Resolver.Resolve(ctx, request.CategoryID, ev)
}

// ResolveNamed implements works.UnitPlacer: an id of the type's providers a person named.
func (p placing) ResolveNamed(ctx context.Context, categoryID int64, named batch.Named) (int64, error) {
	if !slices.Contains(allProviders, named.Provider) {
		return 0, fmt.Errorf("%w: %s %q is not a comic provider id", media.ErrInvalidAdd, named.Provider, named.ExternalID)
	}
	if p.config.Exclusions != nil {
		if err := p.config.Exclusions.Allow(ctx, MediaType, named.Provider, named.ExternalID); err != nil {
			return 0, err
		}
	}
	return p.config.Resolver.ByIDs(ctx, categoryID, map[string]string{named.Provider: named.ExternalID}), nil
}

// ByIDs is the series the ids name: the held one, else the one the providers give, added unmonitored.
func (r Resolver) ByIDs(ctx context.Context, categoryID int64, ids map[string]string) int64 {
	if id, held := r.Service.WorkByIDs(ctx, ids); held {
		return id
	}
	return r.add(ctx, categoryID, ids, Hint{})
}

// importFile is one file of a unit with what placing it needs.
type importFile struct {
	file  inspect.File
	facts Facts
	keys  []library.PartKey
}

// Place implements works.UnitPlacer: each file of the unit goes to the parts its name or metadata gives; a part that
// already has a file takes another only as an upgrade.
func (p placing) Place(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	now := p.config.Clock.Now()
	files := comicFiles(unit)
	if len(files) == 0 {
		return nil, batch.ErrNothingToPlace
	}
	manga := isMangaKind(p.kindOf(request.CategoryID))
	var chosen []importFile
	for _, file := range files {
		absolute := path.Join(request.SourcePath, file.RelPath)
		if rejection := rules.Unpacking(absolute, false); rejection != nil {
			return nil, &batch.RejectedError{MediaType: MediaType, Rejections: []model.Rejection{*rejection}}
		}
		facts, _ := Probe(ctx, p.config.FS, absolute)
		claims := append(claimsOfPath(file.RelPath, facts), downloadClaims(request)...)
		keys := PartKeys(claims, manga)
		if len(keys) == 0 {
			continue
		}
		if manga {
			p.config.Resolver.Service.EnsureParts(ctx, request.CategoryID, workID, keys)
		}
		held, err := p.holdsAll(ctx, workID, keys)
		if err != nil {
			return nil, err
		}
		if held && !upgrade {
			continue
		}
		chosen = append(chosen, importFile{file: file, facts: facts, keys: keys})
	}
	if len(chosen) == 0 {
		return nil, batch.ErrNothingToPlace
	}
	return p.placeFiles(ctx, request, chosen, workID, upgrade, now)
}

func (p placing) kindOf(categoryID int64) string { return p.config.Resolver.Service.kind(categoryID) }

// holdsAll says every part the keys name already has a file.
func (p placing) holdsAll(ctx context.Context, workID int64, keys []library.PartKey) (bool, error) {
	parts, missing, err := p.config.Series.ResolveParts(ctx, workID, keys)
	if err != nil {
		return false, err
	}
	if missing > 0 || len(parts) == 0 {
		return false, nil
	}
	covered, err := p.config.Resolver.Service.Reader.CoveredRefs(ctx, workID)
	if err != nil {
		return false, err
	}
	for _, part := range parts {
		if !covered[part.MediaRef] {
			return false, nil
		}
	}
	return true, nil
}

func (p placing) placeFiles(ctx context.Context, request batch.Request, chosen []importFile, workID int64, upgrade bool, now time.Time) (*library.Placement, error) {
	placerFiles := make([]placer.File, len(chosen))
	var total int64
	for index, each := range chosen {
		placerFiles[index] = placer.File{RelPath: each.file.RelPath, SizeBytes: each.file.SizeBytes}
		total += each.file.SizeBytes
	}
	hashes, err := p.config.Placer.CheckBlocked(ctx, request.SourcePath, placerFiles)
	if err != nil {
		return nil, err
	}
	detail, err := p.config.Reader.GetOf(ctx, MediaType, request.CategoryID, workID, now)
	if err != nil {
		return nil, fmt.Errorf("comic %d: %w", workID, err)
	}
	names, fileManagement := p.namingFor(request.CategoryID)
	destRoot, err := p.config.Placer.ChooseRoot(ctx, request.CategoryID, workID, affinityKey(detail.Summary.Title), total)
	if err != nil {
		return nil, err
	}
	methods := p.config.Placer.Methods(request.Ladder)
	var placed []placer.Placed
	var registered []library.RegisteredFile
	undo := func(cause error) (*library.Placement, error) {
		return nil, errors.Join(cause, p.config.Placer.Undo(placed), p.config.Physical.Unregister(context.WithoutCancel(ctx), registered))
	}
	physical := make([]library.PhysicalFile, 0, len(chosen))
	owners := make([]placer.Owner, 0, len(chosen))
	for index, each := range chosen {
		slot, named := namingOf(detail, each)
		destRel, _, err := names.RenderFile(slot, named, each.file.RelPath)
		if err != nil {
			return undo(fmt.Errorf("name %q: %w", each.file.RelPath, err))
		}
		if upgrade || slices.ContainsFunc(placed, func(done placer.Placed) bool { return done.Rel == destRel }) {
			destRel = p.config.Placer.FreeName(destRoot.Path, destRel)
		}
		sourceAbs, destAbs := path.Join(request.SourcePath, each.file.RelPath), path.Join(destRoot.Path, destRel)
		method, err := mover.PlaceOneOnDisk(ctx, p.config.FS, sourceAbs, destAbs, each.file.SizeBytes, methods)
		if err != nil {
			return undo(fmt.Errorf("place %q: %w", each.file.RelPath, err))
		}
		placed = append(placed, placer.Placed{Source: sourceAbs, Dest: destAbs, Rel: destRel, Hash: hashes[index], Size: each.file.SizeBytes, Method: method})
		if method != mover.PlaceSymlink {
			p.config.Placer.Finish(destRoot, destAbs, fileManagement, time.Time{})
		}
		physical = append(physical, library.PhysicalFile{RelPath: destRel, SizeBytes: each.file.SizeBytes, QuickHash: hashes[index], FirstSeenAt: now})
		owners = append(owners, placer.Owner{SourceRel: each.file.RelPath, DestRel: destRel})
	}
	registered, err = p.config.Physical.RegisterBatch(ctx, destRoot.ID, physical)
	if err != nil {
		return undo(fmt.Errorf("register placed files: %w", err))
	}
	placement := &library.Placement{WorkID: workID}
	links := make([]library.SeriesFileLink, len(registered))
	for index, entry := range registered {
		placement.MediaObjectIDs = append(placement.MediaObjectIDs, entry.MediaObjectID)
		placement.LibraryPathIDs = append(placement.LibraryPathIDs, entry.LibraryPathID)
		placement.TransferMethods = append(placement.TransferMethods, string(placed[index].Method))
		links[index] = library.SeriesFileLink{MediaObjectID: entry.MediaObjectID, Parts: chosen[index].keys}
	}
	if _, err := p.config.Series.AttachFiles(ctx, now, workID, destRoot.ID, links); err != nil {
		return undo(err)
	}
	for index, entry := range registered {
		recordPageFacts(ctx, p.config.Physical, entry.MediaObjectID, path.Ext(chosen[index].file.RelPath), chosen[index].facts, now.Unix())
	}
	if p.config.ExtraFiles != nil {
		placement.Extras = p.config.Placer.Extras(ctx, p.config.ExtraFiles(request.CategoryID), request.SourcePath, destRoot, nil, owners[:1], methods)
	}
	return placement, nil
}

func (p placing) namingFor(categoryID int64) (Naming, settings.FileManagement) {
	profile, fileManagement, mayRename := works.CategoryNaming(p.config.Categories, categoryID, p.config.RenameOnImport)
	names := NamingForKind(profile, p.kindOf(categoryID))
	names.RenameFiles = names.RenameFiles && mayRename
	return names, fileManagement
}

// affinityKey keeps a series' files in one folder on one root.
func affinityKey(title string) string {
	clean := strings.ToLower(strings.TrimSpace(title))
	if clean == "" {
		return ""
	}
	return MediaType + ":" + clean
}

// PlacementKey is the affinity key a series places under, for a root chosen before its first file.
func PlacementKey(title string) string { return affinityKey(title) }

// namingOf is the slot and the facts a file's name draws on: the series, and the first part the file holds.
func namingOf(detail library.SeriesDetailRow, each importFile) (string, FileNaming) {
	named := FileNaming{
		Series: detail.Summary.Title, Year: int(detail.Summary.Year), Publisher: detail.Summary.Fields["publisher"],
		Format: detail.Summary.Fields[FieldFormat], ComicVineID: detail.IDs[ProviderComicVine], MetronID: detail.IDs[ProviderMetron],
		OriginalFilename: strings.TrimSuffix(path.Base(each.file.RelPath), path.Ext(each.file.RelPath)),
	}
	if info := embedded(each.facts); info != nil && info.Year > 0 {
		named.ReleaseYear = info.Year
	}
	claims := claimsOfPath(each.file.RelPath, each.facts)
	for _, claim := range claims {
		if claim.Group != "" {
			named.ReleaseGroup = claim.Group
			break
		}
	}
	first, last := each.keys[0], each.keys[len(each.keys)-1]
	kind, number, _ := strings.Cut(first.MediaRef, ":")
	_, lastNumber, _ := strings.Cut(last.MediaRef, ":")
	printed := number
	if lastNumber != number {
		printed = number + "-" + lastNumber
	}
	switch kind {
	case PartVolume:
		named.Volume, _ = strconv.Atoi(number)
		if lastNumber != number {
			named.VolumeLast, _ = strconv.Atoi(lastNumber)
		}
		return SlotVolumeFile, named
	case PartChapter:
		named.Chapter = printed
		return SlotChapterFile, named
	case PartAnnual:
		named.Issue, named.Format = printed, "annual"
	case PartSpecial:
		named.Issue, named.Format = printed, "special"
	default:
		named.Issue = printed
	}
	return SlotIssueFile, named
}

// recordPageFacts stores the page facts of a placed file where the quality step reads them.
func recordPageFacts(ctx context.Context, physical library.Physical, mediaObjectID int64, extension string, facts Facts, at int64) {
	pages := facts.Pages
	claim := library.AttributeClaim{Container: strings.TrimPrefix(strings.ToLower(extension), "."), ImageFormat: pages.Format, PageCount: pages.Count}
	scalars := library.MeasuredScalars{Width: pages.MedianShort, Height: pages.MedianLong, ProbeVersion: 1}
	_ = physical.RecordMeasured(ctx, mediaObjectID, claim, scalars, "", at)
}

// allProviders are the series ids a person may name for a file.
var allProviders = []string{ProviderComicVine, ProviderMetron, ProviderGCD, ProviderAniListManga, ProviderMAL, ProviderMangaUpdates, ProviderMangaDex}

var (
	_ batch.SizeFloor = Importer{}
	_ batch.BookFiles = Importer{}
	_ batch.Fitter    = Importer{}
)

// MediaSizeFloorBytes implements batch.SizeFloor: a comic archive weighs at least a few pages.
func (Importer) MediaSizeFloorBytes() int64 { return 1 << 10 }

// TakesBookFiles implements batch.BookFiles: the archives of a download are its media.
func (Importer) TakesBookFiles() bool { return true }

// Fit implements batch.Fitter: only comic archives are a comic's; a pdf may as well be a book.
func (Importer) Fit(unit inspect.Unit) float64 {
	fit := batch.FitCertain
	for _, file := range unit.Media {
		switch {
		case !Includes(file.RelPath):
			return 0
		case strings.EqualFold(path.Ext(file.RelPath), ".pdf"):
			fit = batch.FitPossible
		}
	}
	return fit
}
