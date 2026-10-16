package tv

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Module is TV as the media registry sees it. The zero value declares the type; Sweep makes it scannable.
type Module struct {
	Sweep scan.Sweep
	// Hunt searches for one wanted season and sends the best release; nil leaves TV unhunted.
	Hunt func(ctx context.Context, itemID int64) (plan.Outcome, error)
	// Adder looks a work up and adds it; nil leaves the type without an add.
	Adder media.Adding
	// Files places a library file nothing identified; nil leaves such files to wait.
	Files media.PlacingFiles
	// Pictures lists and keeps the providers' pictures of a work; nil leaves the picture unchosen.
	Pictures media.Picturing
	// Refresh reads one series again from its sources; nil leaves it unrefreshable.
	Refresh func(ctx context.Context, workID int64) (duplicate bool, err error)
	// Feed is TV's part of the RSS sync.
	Feed
	// CutoffJob is TV's part of the cutoff check.
	CutoffJob
	// Retry identifies again the files that waited for review; nil leaves the job unregistered.
	Retry *RetryJob
	// ServerFiles writes Jellyfin and Kodi files; nil leaves the job unregistered.
	ServerFiles *ServerFilesJob
	// SharedFiles places episodes two series share; nil leaves the job unregistered.
	SharedFiles *SharedFilesJob
	// Renumber numbers a series by another of TheTVDB's orderings; nil offers none.
	Renumber media.Renumbering
	// Following is TV's part of a follow.
	Following
	// Profiling is TV's part of a person's page.
	Profiling
	// Passes are the type's periodic metadata passes.
	media.Passes
	// Searching is TV's part of a search a person asks for.
	Searching
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

// FiltersReleases implements media.ReleaseFiltering: the TV judge applies a series' release filters.
func (Module) FiltersReleases() {}

// AddWork implements media.Adding.
func (m Module) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	if m.Adder == nil {
		return media.AddResult{}, media.ErrNotWired
	}
	return m.Adder.AddWork(ctx, request)
}

// PlaceFile implements media.PlacingFiles.
func (m Module) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	if m.Files == nil {
		return 0, 0, media.ErrNotWired
	}
	return m.Files.PlaceFile(ctx, categoryID, mediaObjectID, placement, wholeFolder)
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

// NumberingChoices implements media.Renumbering.
func (m Module) NumberingChoices(ctx context.Context, workID int64) ([]media.Choice, string, error) {
	if m.Renumber == nil {
		return nil, "", media.ErrNotWired
	}
	return m.Renumber.NumberingChoices(ctx, workID)
}

// PlanNumbering implements media.Renumbering.
func (m Module) PlanNumbering(ctx context.Context, workID int64, basis string) (media.NumberingPlan, error) {
	if m.Renumber == nil {
		return media.NumberingPlan{}, media.ErrNotWired
	}
	return m.Renumber.PlanNumbering(ctx, workID, basis)
}

// ApplyNumbering implements media.Renumbering.
func (m Module) ApplyNumbering(ctx context.Context, workID int64, basis string) (media.NumberingPlan, error) {
	if m.Renumber == nil {
		return media.NumberingPlan{}, media.ErrNotWired
	}
	return m.Renumber.ApplyNumbering(ctx, workID, basis)
}

// HuntSearch implements media.Hunting.
func (m Module) HuntSearch(ctx context.Context, itemID int64) (plan.Outcome, error) {
	if m.Hunt == nil {
		return plan.OutcomeNotWanted, nil
	}
	return m.Hunt(ctx, itemID)
}

// ReleaseLabel implements media.Labelling.
func (Module) ReleaseLabel(releaseTitle, itemRef string, now time.Time) string {
	return ReleaseLabel(releaseTitle, itemRef, now)
}

// RegisterJobs implements media.Jobs.
func (m Module) RegisterJobs(register media.JobRegistrar) error {
	if m.Retry != nil {
		if err := register(RetryDefinition, m.Retry); err != nil {
			return err
		}
	}
	if m.SharedFiles != nil {
		if err := register(SharedFilesDefinition, m.SharedFiles); err != nil {
			return err
		}
	}
	if m.ServerFiles != nil {
		return register(ServerFilesDefinition, m.ServerFiles)
	}
	return nil
}

// Descriptor implements media.Module.
func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:   media.Key(MediaType),
		Label: "TV",
		Vocabulary: media.Vocabulary{
			Work:         media.Term{One: "Series", Many: "Series"},
			Part:         media.Term{One: "Episode", Many: "Episodes"},
			Group:        media.Term{One: "Season", Many: "Seasons"},
			LeadPeople:   media.Term{One: "Cast member", Many: "Cast"},
			Organisation: media.Term{One: "Network", Many: "Networks"},
		},
		Levels: Navigation{}.Levels(),
		Roles: []media.Role{
			{Key: "actor", Label: "Cast", Followable: true, Character: true},
			{Key: "creator", Label: "Creator", Followable: true},
			{Key: "showrunner", Label: "Showrunner", Followable: true},
			{Key: "executive_producer", Label: "Executive producer", Followable: true},
			{Key: "producer", Label: "Producer", Followable: true},
			{Key: "composer", Label: "Composer", Followable: true},
			{Key: "director", Label: "Director", Followable: true, PartScoped: true},
			{Key: "writer", Label: "Writer", Followable: true, PartScoped: true},
			{Key: "guest_star", Label: "Guest star", PartScoped: true, Character: true},
		},
		FollowOptions:  followOptions,
		Facets:         facets,
		MonitorOptions: monitorChoices,
		WorkImage:      media.ImagePoster,
		WantedGrain:    media.WantedParts,
		AddKinds:       []media.Choice{{Key: "series", Label: "Series"}},
		// The consumer refuses to add an excluded series (Service.excluded).
		CommitsHeldImports:    true,
		ScanHonoursExclusions: true,
		// Sonarr brings subtitles along; a hand-named folder guessed below this is named by a person instead.
		ExtraFiles: media.ExtraFilesDefault{Import: true, Extensions: []string{"srt"}},
		GuessFloor: GuessFloor,
		Kinds:      libraryKinds,
	}
}

// GuessFloor is the least confidence a held folder's guessed series is placed at by "Commit anyway": a folder named by
// hand guesses wrongly at 60% (seen: "my recordings" took a series with "Recordings" in its name).
const GuessFloor = 0.75

// NamingScheme implements media.Naming.
func (Module) NamingScheme() naming.Scheme { return NamingScheme }

// DefaultNamingProfile implements media.Naming.
func (Module) DefaultNamingProfile() settings.NamingProfile { return DefaultNamingProfile() }

// ValidateNaming implements media.Naming.
func (Module) ValidateNaming(profile settings.NamingProfile) error { return ValidateNaming(profile) }

// NamingExamples implements media.Naming.
func (Module) NamingExamples(profile settings.NamingProfile) []media.NamingExample {
	var out []media.NamingExample
	for _, example := range NamingExamples(NamingFor(&profile)) {
		out = append(out, media.NamingExample{Label: example.Label, Path: example.Path})
	}
	return out
}

// PartPosition implements media.Numbering: "s03:e07" is episode 7, its season being the group.
func (Module) PartPosition(ref string) (section, number string) {
	var season, episode int
	if _, err := fmt.Sscanf(ref, "s%d:e%d", &season, &episode); err != nil {
		return "", ref
	}
	return "", strconv.Itoa(episode)
}

// GroupLabel implements media.Grouping: "s03" is "Season 3", "s00" the specials.
func (Module) GroupLabel(ref string) string {
	season, err := strconv.Atoi(strings.TrimPrefix(ref, "s"))
	switch {
	case err != nil || !strings.HasPrefix(ref, "s"):
		return ref
	case season == 0:
		return "Specials"
	default:
		return "Season " + strconv.Itoa(season)
	}
}

// monitorChoices are Sonarr's monitor options, as a series' Monitor menu offers them.
var monitorChoices = []media.Choice{
	{Key: string(MonitorAll), Label: "All episodes"}, {Key: string(MonitorFuture), Label: "Future episodes"},
	{Key: string(MonitorMissing), Label: "Missing episodes"}, {Key: string(MonitorExisting), Label: "Existing episodes"},
	{Key: string(MonitorRecent), Label: "Recent episodes"}, {Key: string(MonitorPilot), Label: "Pilot episode"},
	{Key: string(MonitorFirstSeason), Label: "First season"}, {Key: string(MonitorLastSeason), Label: "Last season"},
	{Key: string(MonitorNone), Label: "None"},
}

// ApplyMonitorOption implements media.Monitoring.
func (m Module) ApplyMonitorOption(ctx context.Context, categoryID, workID int64, option string, withSpecials bool) error {
	if !MonitorOption(option).Valid() {
		return fmt.Errorf("%w: %q", media.ErrUnknownMonitorOption, option)
	}
	return m.Service.ApplyMonitor(ctx, categoryID, workID, MonitorOption(option), withSpecials)
}
