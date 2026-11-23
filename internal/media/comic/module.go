package comic

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// MediaType is the key of the comic type in every table and API.
const MediaType = "comic"

// Level keys, stable across releases: URLs and last-visited preferences reference them.
const (
	LevelSeries     = "series"
	LevelPeople     = "people"
	LevelArcs       = "arcs"
	LevelPublishers = "publishers"
)

// Role keys of the credits a comic carries; a manga's story and art are the writer's and the artist's.
const (
	RoleWriter      = "writer"
	RolePenciller   = "penciller"
	RoleInker       = "inker"
	RoleColorist    = "colorist"
	RoleLetterer    = "letterer"
	RoleCoverArtist = "cover_artist"
	RoleEditor      = "editor"
)

// Module is the comic media type as the media registry sees it; a zero value declares the type, the wiring brings its services.
type Module struct {
	// Registrations are the job types the type brings, with their handlers.
	Registrations []media.JobRegistration
	// Wiring is the add, pictures, refresh and hunt services.
	media.Wiring
	// Passes are the type's periodic metadata passes.
	media.Passes
	// Service applies a monitor option to a series a library holds.
	Service Service
	// Sweep is the scan of the type's files.
	Sweep scan.Sweep
	// Feed is the type's part of the RSS sync.
	Feed
	// CutoffJob is the type's part of the cutoff check.
	CutoffJob
	// Searching is the type's part of a search a person asks for (NewSearching).
	works.Searching
	// Files places a queued file by a person's word.
	Files media.PlacingFiles
	// ReaderFiles writes and previews the reader files of a library.
	ReaderFiles *ReaderFiles
	// Rename names a series' files for a rename; nil leaves comics without one.
	Rename *RenameConfig
}

var _ media.Renaming = Module{}

// NameFiles implements media.Renaming.
func (m Module) NameFiles(ctx context.Context, category settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	if m.Rename == nil {
		return nil, media.ErrNotWired
	}
	return m.Rename.NameFiles(ctx, category, workID, files)
}

var _ media.PlacingFiles = Module{}

// PlaceFile implements media.PlacingFiles.
func (m Module) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	if m.Files == nil {
		return 0, 0, media.ErrNotWired
	}
	return m.Files.PlaceFile(ctx, categoryID, mediaObjectID, placement, wholeFolder)
}

var (
	_ media.Module          = Module{}
	_ media.Naming          = Module{}
	_ media.Jobs            = Module{}
	_ media.Scanning        = Module{}
	_ media.Grouping        = Module{}
	_ media.Numbering       = Module{}
	_ media.Monitoring      = Module{}
	_ media.Adding          = Module{}
	_ media.Refreshing      = Module{}
	_ media.Enriching       = Module{}
	_ media.RecordConsuming = Module{}
)

// GroupLabel implements media.Grouping: the heading a page gives a kind of part.
func (Module) GroupLabel(ref string) string {
	switch ref {
	case PartIssue:
		return "Issues"
	case PartAnnual:
		return "Annuals"
	case PartSpecial:
		return "Specials"
	case PartVolume:
		return "Volumes"
	case PartChapter:
		return "Chapters not yet in a volume"
	}
	return ref
}

// PartPosition implements media.Numbering: a part is shown by the number as printed, with no heading of its own.
func (Module) PartPosition(ref string) (section, number string) {
	_, number, _ = strings.Cut(ref, ":")
	return "", number
}

// ScanSweep implements media.Scanning.
func (m Module) ScanSweep() scan.Sweep { return m.Sweep }

// RegisterJobs implements media.Jobs.
func (m Module) RegisterJobs(register media.JobRegistrar) error {
	return media.RegisterAll(register, m.Registrations)
}

// ApplyMonitorOption implements media.Monitoring.
func (m Module) ApplyMonitorOption(ctx context.Context, _, workID int64, option string, _ bool) error {
	if !MonitorOption(option).Valid() {
		return fmt.Errorf("%w: %q", media.ErrUnknownMonitorOption, option)
	}
	return m.Service.ApplyMonitor(ctx, workID, MonitorOption(option))
}

// NamingScheme implements media.Naming.
func (Module) NamingScheme() naming.Scheme { return NamingScheme() }

// DefaultNamingProfile implements media.Naming.
func (Module) DefaultNamingProfile() settings.NamingProfile {
	return NamingScheme().DefaultProfile(defaultOptions)
}

// ValidateNaming implements media.Naming.
func (Module) ValidateNaming(profile settings.NamingProfile) error {
	return NamingScheme().ValidateProfile(profile)
}

// NamingExamples implements media.Naming.
func (Module) NamingExamples(profile settings.NamingProfile) []media.NamingExample {
	return NamingExamples(NamingFor(&profile))
}

// Monitor options are Sonarr's wording for a series' issues.
var monitorChoices = []media.Choice{
	{Key: "all", Label: "All issues"}, {Key: "future", Label: "Future issues"},
	{Key: "missing", Label: "Missing issues"}, {Key: "first", Label: "First issue"},
	{Key: "latest", Label: "Latest issue"}, {Key: "none", Label: "None"},
}

// Descriptor implements media.Module.
func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:   media.Key(MediaType),
		Label: "Comics",
		Vocabulary: media.Vocabulary{
			Work:         media.Term{One: "Series", Many: "Series"},
			Part:         media.Term{One: "Issue", Many: "Issues"},
			Group:        media.Term{One: "Volume", Many: "Volumes"},
			LeadPeople:   media.Term{One: "Writer", Many: "Writers"},
			Organisation: media.Term{One: "Publisher", Many: "Publishers"},
		},
		GenreTree: GenreTree,
		Levels:    levels(),
		Roles: []media.Role{
			{Key: RoleWriter, Label: "Writer", Lead: true, Followable: true},
			{Key: RolePenciller, Label: "Penciller", Followable: true},
			{Key: RoleInker, Label: "Inker", Followable: true},
			{Key: RoleColorist, Label: "Colorist", Followable: true},
			{Key: RoleLetterer, Label: "Letterer", Followable: true},
			{Key: RoleCoverArtist, Label: "Cover artist", Followable: true},
			{Key: RoleEditor, Label: "Editor", Followable: true},
		},
		Facets:         facets(),
		MonitorOptions: monitorChoices,
		WorkImage:      media.ImagePoster,
		WantedGrain:    media.WantedParts,
		AddKinds:       []media.Choice{{Key: AddKindSeries, Label: "Series"}},
		Kinds:          Kinds(),
		ExtraFiles:     media.ExtraFilesDefault{Import: true, Extensions: []string{"jpg", "xml", "json"}},
		GuessFloor:     GuessFloor,
		FileSettings:   fileSettings,
		// A held import folder is committed by hand, and a removed series' files are left out by the scan.
		CommitsHeldImports:    true,
		ScanHonoursExclusions: true,
		UnidentifiedRetry:     media.IdentifyRetryKey,
	}
}

func levels() []model.Level {
	return []model.Level{
		{Key: LevelSeries, Label: "Series", Entity: model.LevelEntityWork, DefaultSort: "title"},
		{Key: LevelPeople, Label: "Creators", Entity: model.LevelEntityPerson, DefaultSort: "name", Role: RoleWriter},
		{Key: LevelArcs, Label: "Story arcs", Entity: model.LevelEntityCollection, DefaultSort: "name"},
		{Key: LevelPublishers, Label: "Publishers", Entity: model.LevelEntityOrganisation, DefaultSort: "name"},
	}
}

// formatChoices lists the formats a series can have, for the library's Format filter (Trades are the collected ones).
func formatChoices() []media.Choice {
	return []media.Choice{
		{Key: FormatSeries, Label: "Series"}, {Key: FormatLimited, Label: "Limited series"}, {Key: FormatOneShot, Label: "One-shot"},
		{Key: FormatAnnual, Label: "Annual"}, {Key: FormatTradePaperbck, Label: "Trade paperback"}, {Key: FormatHardcover, Label: "Hardcover"},
		{Key: FormatGraphicNovel, Label: "Graphic novel"}, {Key: FormatOmnibus, Label: "Omnibus"}, {Key: FormatGallery, Label: "Gallery"},
	}
}

// PreviewableSettings implements media.FilePreviewing.
func (Module) PreviewableSettings() []media.FileSettingKey {
	return []media.FileSettingKey{media.FileReaderFiles}
}

// PreviewFileSetting implements media.FilePreviewing: how many files the reader-files pass would write in a mode.
func (m Module) PreviewFileSetting(ctx context.Context, categoryID int64, key media.FileSettingKey, option string) (int, error) {
	if key != media.FileReaderFiles || m.ReaderFiles == nil {
		return 0, media.ErrNotWired
	}
	return m.ReaderFiles.Preview(ctx, categoryID, option)
}

// CoversParts implements media.PartCoverage: a collected edition stands for the issues it collects.
func (Module) CoversParts() {}

func facets() []media.Facet {
	return []media.Facet{
		{
			Key: "year", Label: "Year", Entity: model.LevelEntityWork, Kind: media.FacetNumber,
			Source: media.FacetSource{Kind: media.SourceColumn, Name: "year"}, Column: true, Sortable: true, Filterable: true,
		},
		{
			Key: "publisher", Label: "Publisher", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceOrganisation, Name: "publisher"}, Column: true, Sortable: true, Filterable: true,
		},
		{
			Key: "format", Label: "Format", Entity: model.LevelEntityWork, Kind: media.FacetEnum,
			Source: media.FacetSource{Kind: media.SourceField, Name: FieldFormat}, Choices: formatChoices(), Column: true, Sortable: true, Filterable: true,
		},
		{
			Key: "original_language", Label: "Language", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceColumn, Name: "original_language"}, Filterable: true,
		},
	}
}

// The library settings of the comic type, kept in the library's module options by these keys.
const (
	DateTypeStore = "store"
	DateTypeCover = "cover"
	// ReaderFilesOff writes nothing for Kavita and Komga; Fill adds what is missing; Replace overwrites what differs.
	ReaderFilesOff     = "off"
	ReaderFilesFill    = "fill"
	ReaderFilesReplace = "replace"
	EditionOn          = "on"
	EditionOff         = "off"
)

var fileSettings = []media.FileSetting{
	{Key: media.FileDateType, Label: "Date type", Help: "Which date an issue comes out on: the date it is in the shops (the pull list and when it is searched for), or the cover date printed on it.", Options: []media.Choice{
		{Key: DateTypeStore, Label: "Store date"}, {Key: DateTypeCover, Label: "Cover date"}}},
	{Key: media.FileEditionSatisfies, Label: "A collected edition satisfies its issues", Help: "A trade paperback or omnibus you hold stands for the issues it collects: they are not searched for, and the series page shows them as held in the edition.", Options: []media.Choice{
		{Key: EditionOff, Label: "No"}, {Key: EditionOn, Label: "Yes"}}},
	{Key: media.FileReaderFiles, Label: "Kavita and Komga files", Help: "Write what comic readers use: series.json beside the series, and ComicInfo.xml inside each cbz. Fill adds a missing file or element and never changes an existing value; Replace overwrites. A cbz is rewritten only when it would change.", Options: []media.Choice{
		{Key: ReaderFilesOff, Label: "Off"}, {Key: ReaderFilesFill, Label: "Fill what is missing"}, {Key: ReaderFilesReplace, Label: "Replace"}}},
}
