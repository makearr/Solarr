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
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Item states the cutoff check and the search move a book between.
const (
	ItemStateUpgradable = "UPGRADABLE"
	ItemStateComplete   = "COMPLETE"
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

func (i Importer) format() Format { return i.config.Resolver.Service.Format }

// MediaSizeFloorBytes implements batch.SizeFloor.
func (i Importer) MediaSizeFloorBytes() int64 { return i.format().SizeFloorBytes }

// TakesBookFiles implements batch.BookFiles: an ebook download's files are its media.
func (i Importer) TakesBookFiles() bool { return !i.format().ByFolder }

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
	_ batch.BookFiles     = Importer{}
)

var errNothingToPlace = errors.New("book: the unit holds no file the book still wants")

// rejectedError says the unit's files were refused by the import rules.
type rejectedError struct{ Rejections []model.Rejection }

func (e *rejectedError) Error() string {
	names := make([]string, len(e.Rejections))
	for index, rejection := range e.Rejections {
		names[index] = rejection.Rule
	}
	return "book: refused by the import rules: " + strings.Join(names, ", ")
}

// ImportBatch implements batch.Importer.
func (i Importer) ImportBatch(ctx context.Context, request batch.Request) (batch.Result, error) {
	result := batch.Result{
		Decisions:  make([]library.UnitDecision, len(request.Units)),
		Placements: make([]*library.Placement, len(request.Units)),
	}
	var seeded int64
	var upgrade bool
	if request.AcquisitionID != nil {
		seeded, upgrade = i.seededBook(ctx, *request.AcquisitionID)
	}
	for index, unit := range request.Units {
		decision := library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitQueue, Reason: "no book of the library was found in it"}
		workID, candidates := seeded, []model.Identification(nil)
		if workID == 0 {
			workID, candidates = i.resolve(ctx, request, unit)
		}
		if workID <= 0 {
			result.Decisions[index] = decision
			result.Decisions[index].CandidatesJSON = library.CandidatesJSON(candidates)
			continue
		}
		decision.WorkID = &workID
		if request.Preview {
			decision.Reason = batch.ReasonPreview
			result.Decisions[index] = decision
			continue
		}
		placed, err := i.placeUnit(ctx, request, unit, workID, upgrade && workID == seeded)
		var refused *rejectedError
		switch {
		case errors.Is(err, errNothingToPlace):
			decision.Decision, decision.Reason, decision.WorkID = library.UnitSkip, batch.ReasonNothingNew, nil
			result.Decisions[index] = decision
		case errors.As(err, &refused):
			decision.Reason = refused.Error()
			result.Decisions[index] = decision
		case errors.Is(err, placer.ErrNoRoot):
			result.Decisions[index] = batch.WaitToPlace(decision, workID, err)
		case err != nil:
			i.logger.Warn("book import: could not place unit", slog.String("unit", unit.Directory), slog.Any("error", err))
			decision.Decision, decision.Reason, decision.WorkID = library.UnitFailed, err.Error(), nil
			result.Decisions[index] = decision
		default:
			decision.Decision, decision.Reason, decision.Confidence = library.UnitCommit, "", 1
			result.Decisions[index] = decision
			result.Placements[index] = placed
			if i.config.AfterPlacing != nil {
				i.config.AfterPlacing(ctx, request.CategoryID, workID)
			}
		}
	}
	return result, nil
}

// resolve finds the book a unit holds: from its file's name (an ebook) or its folder's (an audiobook), else from the
// download's own name.
func (i Importer) resolve(ctx context.Context, request batch.Request, unit inspect.Unit) (int64, []model.Identification) {
	files := i.bookFiles(unit)
	if len(files) == 0 {
		return 0, nil
	}
	var candidates []model.Identification
	if info, ok := i.format().Parse(files[0].RelPath); ok {
		workID, found := i.config.Resolver.Resolve(ctx, request.CategoryID, info)
		if workID != 0 {
			return workID, nil
		}
		candidates = found
	}
	if info, ok := parsebook.Parse(path.Base(path.Join(request.SourcePath, unit.Directory))); ok {
		if workID, found := i.config.Resolver.Resolve(ctx, request.CategoryID, info); workID != 0 || len(candidates) == 0 {
			return workID, found
		}
	}
	return 0, candidates
}

// seededBook is the book a download was grabbed for; upgrade says it was below its cutoff.
func (i Importer) seededBook(ctx context.Context, acquisitionID int64) (workID int64, upgrade bool) {
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

// CommitHeld implements batch.HeldCommitter: the unit goes to the book named, else the one it resolved to.
func (i Importer) CommitHeld(ctx context.Context, request batch.HeldRequest) (library.Placement, error) {
	workID := request.WorkID
	if request.As != nil && request.Guess > 0 && request.Guess < GuessFloor {
		return library.Placement{}, batch.ErrGuessTooWeak
	}
	if request.As != nil {
		ids, ok := i.format().NamedIDs(request.As.Provider, request.As.ExternalID)
		if !ok {
			return library.Placement{}, fmt.Errorf("%w: %s %q is not a book id", media.ErrInvalidAdd, request.As.Provider, request.As.ExternalID)
		}
		if i.config.Exclusions != nil {
			if err := i.config.Exclusions.Allow(ctx, i.format().MediaType, request.As.Provider, request.As.ExternalID); err != nil {
				return library.Placement{}, err
			}
		}
		if workID = i.config.Resolver.ByIDs(ctx, request.CategoryID, ids); workID <= 0 {
			return library.Placement{}, batch.ErrUnresolved
		}
	}
	if workID <= 0 {
		return library.Placement{}, batch.ErrUnresolved
	}
	var upgrade bool
	if request.AcquisitionID != nil {
		seeded, upgradable := i.seededBook(ctx, *request.AcquisitionID)
		upgrade = upgradable && workID == seeded
	}
	placed, err := i.placeUnit(ctx, batch.Request{SessionID: request.SessionID, CategoryID: request.CategoryID, SourcePath: request.SourcePath}, request.Unit, workID, upgrade)
	if errors.Is(err, errNothingToPlace) {
		return library.Placement{}, batch.ErrNothingToPlace
	}
	if err != nil {
		return library.Placement{}, err
	}
	if i.config.AfterPlacing != nil {
		i.config.AfterPlacing(ctx, request.CategoryID, workID)
	}
	return *placed, nil
}

// bookFiles are the unit's files of the format, in the order their names sort as chapters ("2" before "10").
func (i Importer) bookFiles(unit inspect.Unit) []inspect.File {
	var files []inspect.File
	for _, file := range unit.Media {
		if i.format().Includes(file.RelPath) {
			files = append(files, file)
		}
	}
	slices.SortStableFunc(files, func(a, b inspect.File) int { return NaturalCompare(a.RelPath, b.RelPath) })
	return files
}

// placeUnit places the book's files: an ebook's file (each format a version of it), or an audiobook's chapters as its
// numbered parts. A book that already has files takes more only as an upgrade of an ebook.
func (i Importer) placeUnit(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	now := i.config.Clock.Now()
	format := i.format()
	files := i.bookFiles(unit)
	if len(files) == 0 {
		return nil, errNothingToPlace
	}
	roots, err := i.config.Identity.WorkFileRoots(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("book %d: find existing roots: %w", workID, err)
	}
	if len(roots) > 0 && (!upgrade || format.ByFolder) {
		return nil, errNothingToPlace
	}
	for _, file := range files {
		if rejection := rules.Unpacking(path.Join(request.SourcePath, file.RelPath), false); rejection != nil {
			return nil, &rejectedError{Rejections: []model.Rejection{*rejection}}
		}
	}
	placerFiles := make([]placer.File, len(files))
	var total int64
	for index, file := range files {
		placerFiles[index] = placer.File{RelPath: file.RelPath, SizeBytes: file.SizeBytes}
		total += file.SizeBytes
	}
	hashes, err := i.config.Placer.CheckBlocked(ctx, request.SourcePath, placerFiles)
	if err != nil {
		return nil, err
	}
	basics, err := i.config.Reader.Basics(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("book %d: %w", workID, err)
	}
	names, fileManagement := i.namingFor(request.CategoryID)
	destRoot, err := i.config.Placer.ChooseRoot(ctx, request.CategoryID, workID, affinityKey(format, basics.Title), total)
	if err != nil {
		return nil, err
	}
	facts, err := NamingFacts(ctx, i.config.Reader, format, request.CategoryID, workID, now)
	if err != nil {
		return nil, err
	}
	methods := i.config.Placer.Methods(request.Ladder)
	var placed []placer.Placed
	undo := func(cause error) (*library.Placement, error) {
		return nil, errors.Join(cause, i.config.Placer.Undo(placed))
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
			destRel = i.config.Placer.FreeName(destRoot.Path, destRel)
		}
		sourceAbs, destAbs := path.Join(request.SourcePath, file.RelPath), path.Join(destRoot.Path, destRel)
		method, err := mover.PlaceOne(i.config.FS, sourceAbs, destAbs, file.SizeBytes, methods)
		if err != nil {
			return undo(fmt.Errorf("place %q: %w", file.RelPath, err))
		}
		placed = append(placed, placer.Placed{Source: sourceAbs, Dest: destAbs, Rel: destRel, Hash: hashes[index], Size: file.SizeBytes, Method: method})
		if method != mover.PlaceSymlink {
			i.config.Placer.Finish(destRoot, destAbs, fileManagement, time.Time{})
		}
		physical = append(physical, library.PhysicalFile{RelPath: destRel, SizeBytes: file.SizeBytes, QuickHash: hashes[index], FirstSeenAt: now})
		owners = append(owners, placer.Owner{SourceRel: file.RelPath, DestRel: destRel})
	}
	registered, err := i.config.Physical.RegisterBatch(ctx, destRoot.ID, physical)
	if err != nil {
		return undo(fmt.Errorf("register placed files: %w", err))
	}
	placement := &library.Placement{WorkID: workID}
	for index, entry := range registered {
		placement.MediaObjectIDs = append(placement.MediaObjectIDs, entry.MediaObjectID)
		placement.LibraryPathIDs = append(placement.LibraryPathIDs, entry.LibraryPathID)
		placement.TransferMethods = append(placement.TransferMethods, string(placed[index].Method))
	}
	if _, err := i.config.Series.AttachWholeFiles(ctx, now, workID, destRoot.ID, placement.MediaObjectIDs); err != nil {
		return undo(err)
	}
	if i.config.ExtraFiles != nil {
		placement.Extras = i.config.Placer.Extras(i.config.ExtraFiles(request.CategoryID), request.SourcePath, destRoot, unit.Other, owners[:1], methods)
	}
	return placement, nil
}

func (i Importer) namingFor(categoryID int64) (Naming, settings.FileManagement) {
	names := i.format().NamingFor(nil)
	fileManagement := settings.DefaultFileManagement
	if category, found := i.config.Categories.Category(categoryID); found {
		names = i.format().NamingFor(category.Naming)
		fileManagement = category.FileManagement
	}
	if i.config.RenameOnImport != nil && !i.config.RenameOnImport() {
		names.RenameFiles = false
	}
	return names, fileManagement
}

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
