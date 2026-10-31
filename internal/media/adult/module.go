package adult

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

// Module is the adult media type as the media registry sees it. The zero value declares the type; Sweep makes it
// scannable.
type Module struct {
	// Registrations are the job types the module brings, with their handlers.
	Registrations []media.JobRegistration
	Sweep         scan.Sweep
	// Adder looks a scene up and adds it; nil leaves the type without an add.
	Adder media.Adding
	// Pictures lists and keeps the providers' pictures of a scene; nil leaves the poster unchosen.
	Pictures media.Picturing
	// Refresh reads one scene again from its sources; nil leaves it unrefreshable.
	Refresh func(ctx context.Context, workID int64) (duplicate bool, err error)
	// Hunt searches for one wanted scene and sends the best release; nil leaves scenes unhunted.
	Hunt func(ctx context.Context, itemID int64) (plan.Outcome, error)
	// Feed is the module's part of the RSS sync.
	Feed
	// CutoffJob is the module's part of the cutoff check.
	CutoffJob
	// Searching is the module's part of a search a person asks for (NewSearching).
	works.Searching
	// Files places a library file nothing identified; nil leaves such files to wait.
	Files media.PlacingFiles
	// Service applies a monitor option to a scene a library holds.
	Service Service
	// Passes are the type's periodic metadata passes.
	media.Passes
	// Rename names a scene's files for a rename; nil leaves scenes without one.
	Rename *RenameConfig
	// Follow adds a performer's or studio's scenes to a library that follows them; nil leaves them unfollowable.
	Follow *Following
}

// NameFiles implements media.Renaming.
func (m Module) NameFiles(ctx context.Context, category settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	if m.Rename == nil {
		return nil, fmt.Errorf("adult: rename is not wired")
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
// FiltersReleases implements media.ReleaseFiltering: the judge applies a scene's release filters.
func (Module) FiltersReleases() {}

func (Module) NamingScheme() naming.Scheme { return NamingScheme }

// DefaultNamingProfile implements media.Naming.
func (Module) DefaultNamingProfile() settings.NamingProfile { return DefaultNamingProfile() }

// ValidateNaming implements media.Naming.
func (Module) ValidateNaming(profile settings.NamingProfile) error { return ValidateNaming(profile) }

// NamingExamples implements media.Naming.
func (Module) NamingExamples(profile settings.NamingProfile) []media.NamingExample {
	return NamingExamples(NamingFor(&profile))
}

// ApplyMonitorOption implements media.Monitoring.
func (m Module) ApplyMonitorOption(ctx context.Context, _, workID int64, option string, _ bool) error {
	if !MonitorOption(option).Valid() {
		return fmt.Errorf("%w: %q", media.ErrUnknownMonitorOption, option)
	}
	return m.Service.ApplyMonitor(ctx, workID, MonitorOption(option))
}

// fileSettings are the Media Management settings a library of scenes has.
func fileSettings() []media.FileSetting {
	return []media.FileSetting{
		{Key: media.FileDeleteEmptyFolders, Label: "Delete Empty Folders", Help: "Delete a studio or scene folder left empty when its files are deleted."},
		{Key: media.FileUnmonitorDeleted, Label: "Unmonitor Deleted Scenes", Help: "A scene whose file is deleted from disk is unmonitored instead of being fetched again."},
		{Key: media.FileImportExtraFiles, Label: "Import Extra Files", Help: "Bring files found beside the scene along with it, such as its poster, subtitles or nfo."},
		{Key: media.FileExtraFileExtensions, Label: "Extra File Extensions", Help: "Comma separated, such as srt,nfo,jpg."},
	}
}

// Descriptor implements media.Module.
func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:       MediaType,
		Label:     "Adult",
		GenreTree: GenreTree,
		Vocabulary: media.Vocabulary{
			Work:         media.Term{One: "Scene", Many: "Scenes"},
			LeadPeople:   media.Term{One: "Performer", Many: "Performers"},
			Organisation: media.Term{One: "Studio", Many: "Studios"},
		},
		Levels: Levels(),
		Roles: []media.Role{
			{Key: RolePerformer, Label: "Performer", Lead: true, Followable: true},
			{Key: RoleDirector, Label: "Director", Followable: true},
		},
		Facets: facets(),
		MonitorOptions: []media.Choice{
			{Key: string(MonitorScene), Label: "Scene only"},
			{Key: string(MonitorNone), Label: "None"},
		},
		WorkImage: media.ImagePoster,
		AddKinds:  []media.Choice{{Key: MediaType, Label: "Scenes"}},
		Kinds:     kinds(),
		// A held import file is committed by hand, and a removed scene's file is left out by the scan.
		CommitsHeldImports:    true,
		ScanHonoursExclusions: true,
		ExtraFiles:            media.ExtraFilesDefault{Import: true, Extensions: []string{"srt", "nfo", "jpg"}},
		GuessFloor:            GuessFloor,
		FileSettings:          fileSettings(),
		ScanMonitor: media.ScanMonitor{Setting: media.ScanMonitorParts, Label: "Monitor scenes found by a scan", Options: []media.Choice{
			{Key: "existing", Label: "Monitored"}, {Key: "none", Label: "Not monitored"},
		}},
	}
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
