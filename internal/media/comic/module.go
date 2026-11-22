package comic

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
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
}

var (
	_ media.Module     = Module{}
	_ media.Naming     = Module{}
	_ media.Jobs       = Module{}
	_ media.Scanning   = Module{}
	_ media.Monitoring = Module{}
	_ media.Adding     = Module{}
	_ media.Refreshing = Module{}
	_ media.Enriching  = Module{}
)

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
		AddKinds:       []media.Choice{{Key: "series", Label: "Series"}},
		Kinds:          Kinds(),
		ExtraFiles:     media.ExtraFilesDefault{Import: true, Extensions: []string{"jpg", "xml", "json"}},
		GuessFloor:     0.9,
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
			Key: "original_language", Label: "Language", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceColumn, Name: "original_language"}, Filterable: true,
		},
	}
}
