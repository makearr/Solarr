package book

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Module is one of the two book types as the media registry sees it. A zero value but its Format declares the type;
// Sweep makes it scannable.
type Module struct {
	Format Format
	// Registrations are the job types the format brings, with their handlers.
	Registrations []media.JobRegistration
	Sweep         scan.Sweep
	// Adder looks a book up and adds it; nil leaves the type without an add.
	Adder media.Adding
	// Pictures lists and keeps the providers' covers of a book; nil leaves the cover unchosen.
	Pictures media.Picturing
	// Refresh reads one book again from its sources; nil leaves it unrefreshable.
	Refresh func(ctx context.Context, workID int64) (duplicate bool, err error)
	// Hunt searches for one wanted book and sends the best release; nil leaves books unhunted.
	Hunt func(ctx context.Context, itemID int64) (plan.Outcome, error)
	// Feed is the format's part of the RSS sync.
	Feed
	// CutoffJob is the format's part of the cutoff check.
	CutoffJob
	// Searching is the format's part of a search a person asks for (Format.NewSearching).
	works.Searching
	// Files places a library file nothing identified; nil leaves such files to wait.
	Files media.PlacingFiles
	// Service applies a monitor option to a book a library holds.
	Service Service
	// Passes are the type's periodic metadata passes.
	media.Passes
	// Authors lists an author's other books on their page; nil leaves the page to the library's own.
	Authors *AuthorWorks
	// Rename names a book's files for a rename; nil leaves books without one.
	Rename *RenameConfig
}

// KnownFor implements media.Profiling.
func (m Module) KnownFor(ctx context.Context, personID, categoryID int64) ([]media.KnownFor, error) {
	if m.Authors == nil {
		return nil, nil
	}
	return m.Authors.KnownFor(ctx, personID, categoryID)
}

// NameFiles implements media.Renaming.
func (m Module) NameFiles(ctx context.Context, category settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	if m.Rename == nil {
		return nil, fmt.Errorf("book: rename is not wired")
	}
	return m.Rename.NameFiles(ctx, category, workID, files)
}

// ScanSweep implements media.Scanning.
func (m Module) ScanSweep() scan.Sweep { return m.Sweep }

// LookupWorks implements media.Adding.
func (m Module) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	if m.Adder == nil {
		return nil, media.ErrNotWired
	}
	return m.Adder.LookupWorks(ctx, lookup)
}

// AddWork implements media.Adding.
func (m Module) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	if m.Adder == nil {
		return media.AddResult{}, media.ErrNotWired
	}
	return m.Adder.AddWork(ctx, request)
}

// WorkPictures implements media.Picturing.
func (m Module) WorkPictures(ctx context.Context, workID int64) ([]media.ImageOption, error) {
	if m.Pictures == nil {
		return nil, media.ErrNotWired
	}
	return m.Pictures.WorkPictures(ctx, workID)
}

// PickWorkPicture implements media.Picturing.
func (m Module) PickWorkPicture(ctx context.Context, workID int64, imageURL string) error {
	if m.Pictures == nil {
		return media.ErrNotWired
	}
	return m.Pictures.PickWorkPicture(ctx, workID, imageURL)
}

// RefreshWork implements media.Refreshing.
func (m Module) RefreshWork(ctx context.Context, workID int64) (bool, error) {
	if m.Refresh == nil {
		return false, media.ErrNotWired
	}
	return m.Refresh(ctx, workID)
}

// HuntSearch implements media.Hunting.
func (m Module) HuntSearch(ctx context.Context, itemID int64) (plan.Outcome, error) {
	if m.Hunt == nil {
		return plan.OutcomeNotWanted, nil
	}
	return m.Hunt(ctx, itemID)
}

// RegisterJobs implements media.Jobs.
func (m Module) RegisterJobs(register media.JobRegistrar) error {
	for _, registration := range m.Registrations {
		if err := register(registration.Definition, registration.Handler); err != nil {
			return err
		}
	}
	return nil
}

// NamingScheme implements media.Naming.
func (m Module) NamingScheme() naming.Scheme { return m.Format.NamingScheme() }

// DefaultNamingProfile implements media.Naming.
func (m Module) DefaultNamingProfile() settings.NamingProfile { return m.Format.DefaultNamingProfile() }

// ValidateNaming implements media.Naming.
func (m Module) ValidateNaming(profile settings.NamingProfile) error {
	return m.Format.ValidateNaming(profile)
}

// NamingExamples implements media.Naming.
func (m Module) NamingExamples(profile settings.NamingProfile) []media.NamingExample {
	return m.Format.NamingExamples(m.Format.NamingFor(&profile))
}

// FiltersReleases implements media.ReleaseFiltering: the book judge applies a book's release filters.
func (Module) FiltersReleases() {}

// ApplyMonitorOption implements media.Monitoring.
func (m Module) ApplyMonitorOption(ctx context.Context, _, workID int64, option string, _ bool) error {
	if !MonitorOption(option).Valid() {
		return fmt.Errorf("%w: %q", media.ErrUnknownMonitorOption, option)
	}
	return m.Service.ApplyMonitor(ctx, workID, MonitorOption(option))
}

// fileSettings are Readarr's Media Management settings as a book library has them.
func (f Format) fileSettings() []media.FileSetting {
	return []media.FileSetting{
		{Key: media.FileDeleteEmptyFolders, Label: "Delete Empty Folders", Help: "Delete an author or book folder left empty when its files are deleted."},
		{Key: media.FileUnmonitorDeleted, Label: "Unmonitor Deleted " + f.Many, Help: "A " + lower(f.One) + " whose files are deleted from disk is unmonitored instead of being fetched again."},
		{Key: media.FileImportExtraFiles, Label: "Import Extra Files", Help: "Bring files found beside the " + lower(f.One) + " along with it, such as its cover or an OPF file."},
		{Key: media.FileExtraFileExtensions, Label: "Extra File Extensions", Help: "Comma separated, such as jpg,opf,cue."},
	}
}

// Descriptor implements media.Module.
func (m Module) Descriptor() media.Descriptor {
	format := m.Format
	roles := []media.Role{
		{Key: RoleAuthor, Label: "Author", Lead: true, Followable: true},
		{Key: RoleTranslator, Label: "Translator", Followable: true},
	}
	if format.ByFolder {
		roles = append(roles, media.Role{Key: RoleNarrator, Label: "Narrator", Followable: true})
	}
	return media.Descriptor{
		Key:       media.Key(format.MediaType),
		Label:     format.Label,
		GenreTree: GenreTree,
		Vocabulary: media.Vocabulary{
			Work:         media.Term{One: format.One, Many: format.Many},
			LeadPeople:   media.Term{One: "Author", Many: "Authors"},
			Organisation: media.Term{One: "Publisher", Many: "Publishers"},
		},
		Levels: format.Levels(),
		Roles:  roles,
		Facets: format.facets(),
		MonitorOptions: []media.Choice{
			{Key: string(MonitorBook), Label: format.One + " only"},
			{Key: string(MonitorBookAndSeries), Label: format.One + " and series"},
			{Key: string(MonitorNone), Label: "None"},
		},
		WorkImage: media.ImagePoster,
		AddKinds:  []media.Choice{{Key: format.MediaType, Label: format.Many}},
		Kinds:     format.kinds(),
		// A held import folder is committed by hand, and a removed book's files are left out by the scan.
		CommitsHeldImports:    true,
		ScanHonoursExclusions: true,
		ExtraFiles:            media.ExtraFilesDefault{Import: true, Extensions: []string{"opf", "jpg", "cue"}},
		GuessFloor:            GuessFloor,
		FileSettings:          format.fileSettings(),
		ScanMonitor: media.ScanMonitor{Setting: media.ScanMonitorParts, Label: "Monitor " + lower(format.Many) + " found by a scan", Options: []media.Choice{
			{Key: "existing", Label: "Monitored"}, {Key: "none", Label: "Not monitored"},
		}},
	}
}
