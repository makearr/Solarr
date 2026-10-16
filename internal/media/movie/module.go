package movie

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Module is movies as the media registry sees it. The zero value declares the type; Sweep makes it scannable.
type Module struct {
	// TMDBConfigured says the owner has stored a TMDB key; nil leaves the missing-key note out.
	TMDBConfigured func() bool
	// Registrations are the job types movies bring, with their handlers.
	Registrations []media.JobRegistration
	Sweep         scan.Sweep
	// Adder looks a movie up and adds it; nil leaves the type without an add.
	Adder media.Adding
	// Pictures lists and keeps the providers' pictures of a movie; nil leaves the picture unchosen.
	Pictures media.Picturing
	// Refresh reads one movie again from its sources; nil leaves it unrefreshable.
	Refresh func(ctx context.Context, workID int64) (duplicate bool, err error)
	// Service applies a monitor option to a movie a library holds.
	Service Service
	// Passes are the type's periodic metadata passes.
	media.Passes
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

// RegisterJobs implements media.Jobs.
func (m Module) RegisterJobs(register media.JobRegistrar) error {
	for _, registration := range m.Registrations {
		if err := register(registration.Definition, registration.Handler); err != nil {
			return err
		}
	}
	return nil
}

// HealthChecks implements media.Healthy: movies work without a TMDB key through Radarr's service, but less fully.
func (m Module) HealthChecks(libraries int) []media.HealthCheck {
	if libraries == 0 || m.TMDBConfigured == nil || m.TMDBConfigured() {
		return nil
	}
	return []media.HealthCheck{{
		ID: "movie_no_tmdb_key", Severity: media.HealthWarning,
		Message: "Movies work without a TMDB key through Radarr's metadata service, which can be slow or unavailable: add your free TMDB key under Metadata.",
		Link:    "/settings/metadata",
	}}
}

// FiltersReleases implements media.ReleaseFiltering: the movie judge applies a movie's release filters.
func (Module) FiltersReleases() {}

// ApplyMonitorOption implements media.Monitoring.
func (m Module) ApplyMonitorOption(ctx context.Context, categoryID, workID int64, option string, _ bool) error {
	if !MonitorOption(option).Valid() {
		return fmt.Errorf("%w: %q", media.ErrUnknownMonitorOption, option)
	}
	return m.Service.ApplyMonitor(ctx, categoryID, workID, MonitorOption(option))
}

var monitorChoices = []media.Choice{
	{Key: string(MonitorMovieOnly), Label: "Movie only"},
	{Key: string(MonitorMovieAndCollection), Label: "Movie and collection"},
	{Key: string(MonitorNone), Label: "None"},
}

// Descriptor implements media.Module.
func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:   media.Key(MediaType),
		Label: "Movies",
		Vocabulary: media.Vocabulary{
			Work:         media.Term{One: "Movie", Many: "Movies"},
			LeadPeople:   media.Term{One: "Director", Many: "Directors"},
			Organisation: media.Term{One: "Studio", Many: "Studios"},
		},
		Levels: Navigation{}.Levels(),
		Roles: []media.Role{
			{Key: RoleDirector, Label: "Director", Lead: true, Followable: true},
			{Key: RoleActor, Label: "Cast", Followable: true, Character: true},
			{Key: RoleWriter, Label: "Writer", Followable: true},
			{Key: RoleProducer, Label: "Producer", Followable: true},
			{Key: RoleComposer, Label: "Composer", Followable: true},
			{Key: RoleCinematographer, Label: "Cinematographer", Followable: true},
		},
		Facets:         facets,
		MonitorOptions: monitorChoices,
		WorkImage:      media.ImagePoster,
		AddKinds:       []media.Choice{{Key: "movie", Label: "Movies"}},
		// A held import folder is committed by hand, and a removed movie's files are left out by the scan.
		CommitsHeldImports:    true,
		ScanHonoursExclusions: true,
		// Radarr leaves subtitles where they are unless asked; here they come along, as in TV.
		ExtraFiles: media.ExtraFilesDefault{Import: true, Extensions: []string{"srt"}},
		GuessFloor: GuessFloor,
	}
}

// GuessFloor is the least confidence a held folder's guessed movie is placed at by "Commit anyway".
const GuessFloor = 0.75
