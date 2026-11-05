package book

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
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/importing/rules"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// GuessFloor is the least confidence a held folder's guessed book is placed at by "Commit anyway".
const GuessFloor = 0.75

// ImporterConfig is what an Importer needs; Logger, RenameOnImport, ExtraFiles, AfterPlacing and Exclusions are
// optional.
type ImporterConfig struct {
	FS           fsx.FS
	Placer       placer.Placer
	Physical     library.Physical
	Series       library.Series
	Reader       library.SeriesReader
	Identity     library.Identity
	Desired      library.Desired
	Acquisitions batch.AcquisitionSource
	Resolver     Resolver
	Categories   interface {
		Category(id int64) (settings.Category, bool)
	}
	// Exclusions lifts a book's exclusion when a person names it for a held unit.
	Exclusions interface {
		Allow(ctx context.Context, mediaType, provider, externalID string) error
	}
	Clock          clock.Clock
	Logger         *slog.Logger
	RenameOnImport func() bool
	// ExtraFiles lists the extensions of files brought along with the book (its cover, an nfo); nil brings none.
	ExtraFiles func(categoryID int64) []string
	// AfterPlacing is told of each book an import placed files of.
	AfterPlacing func(ctx context.Context, categoryID, workID int64)
}

// Importer is the book module's batch importer: it works out which book a download holds, names and places its files
// below the author's folder, and records them as the book's. What it cannot place waits in the Review queue.
type Importer struct {
	works.Importer
	placing
}

// placing is the book's part of works.Importer.
type placing struct {
	config ImporterConfig
}

// NewImporter builds the importer.
func NewImporter(config ImporterConfig) Importer {
	units := placing{config: config}
	return Importer{works.NewImporter(works.ImporterConfig{
		MediaType: "book", Noun: "book", GuessFloor: GuessFloor, Units: units,
		Acquisitions: config.Acquisitions, Desired: config.Desired, Logger: config.Logger, AfterPlacing: config.AfterPlacing,
	}), units}
}

func (p placing) format() Format { return p.config.Resolver.Service.Format }

// MediaSizeFloorBytes implements batch.SizeFloor.
func (i Importer) MediaSizeFloorBytes() int64 { return i.format().SizeFloorBytes }

// TakesBookFiles implements batch.BookFiles: an ebook download's files are its media.
func (i Importer) TakesBookFiles() bool { return !i.format().ByFolder }

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
	_ batch.SizeFloor     = Importer{}
	_ batch.BookFiles     = Importer{}
)

// Resolve implements works.UnitPlacer: the book from its file's name (an ebook) or its folder's (an audiobook), else
// from the download's own name.
func (p placing) Resolve(ctx context.Context, request batch.Request, unit inspect.Unit) (int64, []model.Identification) {
	files := p.bookFiles(unit)
	if len(files) == 0 {
		return 0, nil
	}
	var candidates []model.Identification
	if info, ok := p.format().Parse(files[0].RelPath); ok {
		workID, found := p.config.Resolver.Resolve(ctx, request.CategoryID, info)
		if workID != 0 {
			return workID, nil
		}
		candidates = found
	}
	if info, ok := parsebook.Parse(path.Base(path.Join(request.SourcePath, unit.Directory))); ok {
		if workID, found := p.config.Resolver.Resolve(ctx, request.CategoryID, info); workID != 0 || len(candidates) == 0 {
			return workID, found
		}
	}
	return 0, candidates
}

// ResolveNamed implements works.UnitPlacer: an id of the format's providers a person named.
func (p placing) ResolveNamed(ctx context.Context, categoryID int64, named batch.Named) (int64, error) {
	ids, ok := p.format().NamedIDs(named.Provider, named.ExternalID)
	if !ok {
		return 0, fmt.Errorf("%w: %s %q is not a book id", media.ErrInvalidAdd, named.Provider, named.ExternalID)
	}
	if p.config.Exclusions != nil {
		if err := p.config.Exclusions.Allow(ctx, p.format().MediaType, named.Provider, named.ExternalID); err != nil {
			return 0, err
		}
	}
	return p.config.Resolver.ByIDs(ctx, categoryID, ids), nil
}

// bookFiles are the unit's files of the format, in the order their names sort as chapters ("2" before "10").
func (p placing) bookFiles(unit inspect.Unit) []inspect.File {
	var files []inspect.File
	for _, file := range unit.Media {
		if p.format().Includes(file.RelPath) {
			files = append(files, file)
		}
	}
	slices.SortStableFunc(files, func(a, b inspect.File) int { return NaturalCompare(a.RelPath, b.RelPath) })
	return files
}

// placeUnit places the book's files: an ebook's file (each format a version of it), or an audiobook's chapters as its
// numbered parts. A book that already has files takes more only as an upgrade of an ebook.
// Place implements works.UnitPlacer.
func (p placing) Place(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	now := p.config.Clock.Now()
	format := p.format()
	files := p.bookFiles(unit)
	if len(files) == 0 {
		return nil, batch.ErrNothingToPlace
	}
	roots, err := p.config.Identity.WorkFileRoots(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("book %d: find existing roots: %w", workID, err)
	}
	if len(roots) > 0 && (!upgrade || format.ByFolder) {
		return nil, batch.ErrNothingToPlace
	}
	for _, file := range files {
		if rejection := rules.Unpacking(path.Join(request.SourcePath, file.RelPath), false); rejection != nil {
			return nil, &batch.RejectedError{MediaType: "book", Rejections: []model.Rejection{*rejection}}
		}
	}
	placerFiles := make([]placer.File, len(files))
	var total int64
	for index, file := range files {
		placerFiles[index] = placer.File{RelPath: file.RelPath, SizeBytes: file.SizeBytes}
		total += file.SizeBytes
	}
	hashes, err := p.config.Placer.CheckBlocked(ctx, request.SourcePath, placerFiles)
	if err != nil {
		return nil, err
	}
	basics, err := p.config.Reader.Basics(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("book %d: %w", workID, err)
	}
	names, fileManagement := p.namingFor(request.CategoryID)
	destRoot, err := p.config.Placer.ChooseRoot(ctx, request.CategoryID, workID, affinityKey(format, basics.Title), total)
	if err != nil {
		return nil, err
	}
	facts, err := NamingFacts(ctx, p.config.Reader, format, request.CategoryID, workID, now)
	if err != nil {
		return nil, err
	}
	methods := p.config.Placer.Methods(request.Ladder)
	var placed []placer.Placed
	undo := func(cause error) (*library.Placement, error) {
		return nil, errors.Join(cause, p.config.Placer.Undo(placed))
	}
	physical := make([]library.PhysicalFile, 0, len(files))
	owners := make([]placer.Owner, 0, len(files))
	for index, file := range files {
		named := facts
		named.PartNumber, named.PartCount = index+1, len(files)
		if !format.ByFolder {
			// Each ebook file is the whole book in its own format, never a part.
			named.PartNumber, named.PartCount = 0, 1
		}
		named.OriginalFilename = strings.TrimSuffix(path.Base(file.RelPath), path.Ext(file.RelPath))
		if info, ok := parsebook.Parse(path.Base(file.RelPath)); ok {
			named.ReleaseGroup = info.ReleaseGroup
		}
		destRel, _, err := names.RenderBook(named, file.RelPath)
		if err != nil {
			return undo(fmt.Errorf("name %q: %w", file.RelPath, err))
		}
		if upgrade || slices.ContainsFunc(placed, func(done placer.Placed) bool { return done.Rel == destRel }) {
			destRel = p.config.Placer.FreeName(destRoot.Path, destRel)
		}
		sourceAbs, destAbs := path.Join(request.SourcePath, file.RelPath), path.Join(destRoot.Path, destRel)
		method, err := mover.PlaceOne(p.config.FS, sourceAbs, destAbs, file.SizeBytes, methods)
		if err != nil {
			return undo(fmt.Errorf("place %q: %w", file.RelPath, err))
		}
		placed = append(placed, placer.Placed{Source: sourceAbs, Dest: destAbs, Rel: destRel, Hash: hashes[index], Size: file.SizeBytes, Method: method})
		if method != mover.PlaceSymlink {
			p.config.Placer.Finish(destRoot, destAbs, fileManagement, time.Time{})
		}
		physical = append(physical, library.PhysicalFile{RelPath: destRel, SizeBytes: file.SizeBytes, QuickHash: hashes[index], FirstSeenAt: now})
		owners = append(owners, placer.Owner{SourceRel: file.RelPath, DestRel: destRel})
	}
	registered, err := p.config.Physical.RegisterBatch(ctx, destRoot.ID, physical)
	if err != nil {
		return undo(fmt.Errorf("register placed files: %w", err))
	}
	placement := &library.Placement{WorkID: workID}
	for index, entry := range registered {
		placement.MediaObjectIDs = append(placement.MediaObjectIDs, entry.MediaObjectID)
		placement.LibraryPathIDs = append(placement.LibraryPathIDs, entry.LibraryPathID)
		placement.TransferMethods = append(placement.TransferMethods, string(placed[index].Method))
	}
	if _, err := p.config.Series.AttachWholeFiles(ctx, now, workID, destRoot.ID, placement.MediaObjectIDs); err != nil {
		return undo(err)
	}
	if p.config.ExtraFiles != nil {
		placement.Extras = p.config.Placer.Extras(p.config.ExtraFiles(request.CategoryID), request.SourcePath, destRoot, unit.Other, owners[:1], methods)
	}
	return placement, nil
}

func (p placing) namingFor(categoryID int64) (Naming, settings.FileManagement) {
	names := p.format().NamingFor(nil)
	fileManagement := settings.DefaultFileManagement
	if category, found := p.config.Categories.Category(categoryID); found {
		names = p.format().NamingFor(category.Naming)
		fileManagement = category.FileManagement
	}
	if p.config.RenameOnImport != nil && !p.config.RenameOnImport() {
		names.RenameFiles = false
	}
	return names, fileManagement
}

// PlacementKey is the affinity key a book of the format places under, for a root chosen before its first file.
func PlacementKey(format Format, title string) string { return affinityKey(format, title) }

// affinityKey keeps a book's files in one folder on one root.
func affinityKey(format Format, title string) string {
	clean := strings.ToLower(strings.TrimSpace(title))
	if clean == "" {
		return ""
	}
	return format.MediaType + ":" + clean
}

// NamingFacts gathers what a book's file names draw on: its title, year, first author and narrator, series and ids.
func NamingFacts(ctx context.Context, reader library.SeriesReader, format Format, categoryID, workID int64, now time.Time) (FileNaming, error) {
	detail, err := reader.GetOf(ctx, format.MediaType, categoryID, workID, now)
	if err != nil {
		return FileNaming{}, fmt.Errorf("book %d: %w", workID, err)
	}
	facts := FileNaming{
		Title: detail.Summary.Title, Year: int(detail.Summary.Year), Subtitle: detail.Summary.Fields[FieldSubtitle],
		Series: detail.Summary.Fields[FieldSeries], SeriesPosition: detail.Summary.Fields[FieldSeriesPosition],
		ISBN: detail.IDs[format.Provider(ProviderISBN)], ASIN: detail.IDs[ProviderAudible],
	}
	credits := slices.Clone(detail.Credits)
	slices.SortStableFunc(credits, func(a, b library.SeriesCreditRow) int { return cmp.Compare(a.Order, b.Order) })
	for _, credit := range credits {
		switch {
		case credit.Role == RoleAuthor && facts.Author == "":
			facts.Author = credit.Name
		case credit.Role == RoleNarrator && facts.Narrator == "":
			facts.Narrator = credit.Name
		}
	}
	if facts.Author == "" {
		facts.Author = "Unknown Author"
	}
	return facts, nil
}

// NaturalCompare orders names as a person numbers chapters: "Part 2" before "Part 10".
func NaturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := leadingDigits(a), leadingDigits(b)
		if da != "" && db != "" {
			na, _ := strconv.ParseInt(da, 10, 64)
			nb, _ := strconv.ParseInt(db, 10, 64)
			if na != nb {
				return cmp.Compare(na, nb)
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(strings.ToLower(a[:1]), strings.ToLower(b[:1]))
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func leadingDigits(text string) string {
	end := 0
	for end < len(text) && end < 18 && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	return text[:end]
}
