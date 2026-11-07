package adult

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

// Module is the adult media type as the media registry sees it. The zero value declares the type; Sweep makes it
// scannable.
type Module struct {
	// Registrations are the job types the module brings, with their handlers.
	Registrations []media.JobRegistration
	// Wiring is the add, pictures, refresh and hunt services.
	media.Wiring
	Sweep scan.Sweep
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
	// Scenes lists a performer's scenes the library lacks; nil leaves the page without them.
	Scenes *PerformerScenes
}

// KnownFor implements media.Profiling.
func (m Module) KnownFor(ctx context.Context, personID, categoryID int64) ([]media.KnownFor, error) {
	if m.Scenes == nil {
		return nil, nil
	}
	return m.Scenes.KnownFor(ctx, personID, categoryID)
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

// RegisterJobs implements media.Jobs.
func (m Module) RegisterJobs(register media.JobRegistrar) error {
	return media.RegisterAll(register, m.Registrations)
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
// RecogniseRelease implements media.RecognisingReleases.
func (Module) RecogniseRelease(title string) []media.ReleaseClaim {
	return works.RecogniseVideoRelease(title)
}

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
		AddKinds:  []media.Choice{{Key: MediaType, Label: "Scenes"}, {Key: KindPerformer, Label: "Performers"}},
		Kinds:     kinds(),
		// A held import file is committed by hand, and a removed scene's file is left out by the scan.
		CommitsHeldImports:    true,
		ScanHonoursExclusions: true,
		UnidentifiedRetry:     media.IdentifyRetryKey,
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

// ImportListTypes implements media.ListResolving: the scenes of a stash-box account's favourites.
func (Module) ImportListTypes() []media.ListType { return []media.ListType{media.ListStashFavourites} }

// ResolveListEntry implements media.ListResolving: an entry is a scene its server names, added by that server's id.
func (Module) ResolveListEntry(_ context.Context, entry media.ListEntry) (media.ListMatch, error) {
	for _, id := range entry.IDs {
		if _, named := NamedIDs(id.Provider, id.ExternalID); named {
			return media.ListMatch{ProviderID: id, Found: true}, nil
		}
	}
	return media.ListMatch{}, nil
}

var _ media.PlacingFiles = Module{}

// PlaceFile implements media.PlacingFiles.
func (m Module) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	if m.Files == nil {
		return 0, 0, media.ErrNotWired
	}
	return m.Files.PlaceFile(ctx, categoryID, mediaObjectID, placement, wholeFolder)
}
