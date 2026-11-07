package book

import (
	"context"
	"fmt"

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
	// Wiring is the add, pictures, refresh and hunt services.
	media.Wiring
	Sweep scan.Sweep
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
	// Follow adds an author's books to a library that follows them; nil leaves authors unfollowable.
	Follow *Following
	// Series lists and adds a series' missing books; nil leaves series collections to the books they hold.
	Series *SeriesBooks
	// Rename names a book's files for a rename; nil leaves books without one.
	Rename *RenameConfig
}

// MissingFranchiseMembers implements media.Franchising.
func (m Module) MissingFranchiseMembers(ctx context.Context, categoryID, franchiseID int64) ([]media.FranchiseMember, error) {
	if m.Series == nil {
		return nil, media.ErrNotWired
	}
	return m.Series.MissingFranchiseMembers(ctx, categoryID, franchiseID)
}

// FranchiseFacts implements media.Franchising.
func (m Module) FranchiseFacts(context.Context, []media.FranchiseRef) map[int64]media.FranchiseFact {
	return nil
}

// MonitorFranchise implements media.Franchising.
func (m Module) MonitorFranchise(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	if m.Series == nil {
		return 0, media.ErrNotWired
	}
	return m.Series.MonitorFranchise(ctx, categoryID, franchiseID, monitored)
}

// FollowCandidates implements media.Following.
func (m Module) FollowCandidates(ctx context.Context, follow media.Follow) ([]media.FollowCandidate, error) {
	if m.Follow == nil {
		return nil, media.ErrNoFollowSource
	}
	return m.Follow.FollowCandidates(ctx, follow)
}

// AddFollowed implements media.Following.
func (m Module) AddFollowed(ctx context.Context, follow media.Follow, candidate media.FollowCandidate) error {
	if m.Follow == nil {
		return media.ErrNoFollowSource
	}
	return m.Follow.AddFollowed(ctx, follow, candidate)
}

// FollowLimit implements media.Following.
func (m Module) FollowLimit(follow media.Follow) int { return Following{}.FollowLimit(follow) }

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

// RegisterJobs implements media.Jobs.
func (m Module) RegisterJobs(register media.JobRegistrar) error {
	return media.RegisterAll(register, m.Registrations)
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
		UnidentifiedRetry:     media.IdentifyRetryKey,
		ExtraFiles:            media.ExtraFilesDefault{Import: true, Extensions: []string{"opf", "jpg", "cue"}},
		GuessFloor:            GuessFloor,
		FileSettings:          format.fileSettings(),
		ScanMonitor: media.ScanMonitor{Setting: media.ScanMonitorParts, Label: "Monitor " + lower(format.Many) + " found by a scan", Options: []media.Choice{
			{Key: "existing", Label: "Monitored"}, {Key: "none", Label: "Not monitored"},
		}},
	}
}

var _ media.PlacingFiles = Module{}

// PlaceFile implements media.PlacingFiles.
func (m Module) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	if m.Files == nil {
		return 0, 0, media.ErrNotWired
	}
	return m.Files.PlaceFile(ctx, categoryID, mediaObjectID, placement, wholeFolder)
}
