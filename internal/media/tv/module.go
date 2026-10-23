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
	"github.com/makearr/Solarr/internal/media/screen"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Module is TV as the media registry sees it. The zero value declares the type; Sweep makes it scannable.
type Module struct {
	// TMDBConfigured says the owner has stored a TMDB key; nil leaves the missing-key check out.
	TMDBConfigured func() bool
	// Registrations are job types TV brings besides the ones named below, with their handlers.
	Registrations []media.JobRegistration
	Sweep         scan.Sweep
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
	// Franchise is TV's part of a series' collections; nil leaves them to hand.
	Franchise media.Franchising
	// FranchisePass finds the franchises series belong to; nil leaves the job unregistered.
	FranchisePass *FranchisePass
	// Crossovers finds the episodes that are one broadcast under two series; nil leaves the job unregistered.
	Crossovers *CrossoverJob
	// Rename names a series' files for a rename; nil leaves TV without one.
	Rename *RenameConfig
}

// RatingAge implements media.Rating.
func (Module) RatingAge(rating string) (int64, bool) { return screen.RatingAge(rating) }

// NameFiles implements media.Renaming.
func (m Module) NameFiles(ctx context.Context, category settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	if m.Rename == nil {
		return nil, fmt.Errorf("tv: rename is not wired")
	}
	return m.Rename.NameFiles(ctx, category, workID, files)
}

// MissingFranchiseMembers implements media.Franchising.
func (m Module) MissingFranchiseMembers(ctx context.Context, categoryID, franchiseID int64) ([]media.FranchiseMember, error) {
	if m.Franchise == nil {
		return nil, media.ErrNotWired
	}
	return m.Franchise.MissingFranchiseMembers(ctx, categoryID, franchiseID)
}

// FranchiseFacts implements media.Franchising.
func (m Module) FranchiseFacts(ctx context.Context, franchises []media.FranchiseRef) map[int64]media.FranchiseFact {
	if m.Franchise == nil {
		return nil
	}
	return m.Franchise.FranchiseFacts(ctx, franchises)
}

// MonitorFranchise implements media.Franchising.
func (m Module) MonitorFranchise(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	if m.Franchise == nil {
		return 0, media.ErrNotWired
	}
	return m.Franchise.MonitorFranchise(ctx, categoryID, franchiseID, monitored)
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

// HealthChecks implements media.Healthy: TMDB, the one TV provider that needs the owner's own key, is not set up.
func (m Module) HealthChecks(libraries int) []media.HealthCheck {
	if libraries == 0 || m.TMDBConfigured == nil || m.TMDBConfigured() {
		return nil
	}
	return []media.HealthCheck{{
		ID: "tv_no_tmdb_key", Severity: media.HealthWarning,
		Message: "TV works without a TMDB key, but cast, crew, guest stars, networks and artwork come from TMDB: add your free key under Metadata.",
		Link:    "/settings/metadata",
	}}
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
	for _, registration := range m.Registrations {
		if err := register(registration.Definition, registration.Handler); err != nil {
			return err
		}
	}
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
	if m.Crossovers != nil {
		if err := register(CrossoverDefinition, m.Crossovers); err != nil {
			return err
		}
	}
	if m.FranchisePass != nil {
		if err := register(FranchisePassDefinition, m.FranchisePass); err != nil {
			return err
		}
	}
	if m.ServerFiles != nil {
		return register(ServerFilesDefinition, m.ServerFiles)
	}
	return nil
}

// Descriptor implements media.Module.
// fileSettings are Sonarr's Media Management settings as a TV library has them, and the media server and crossover ones.
var fileSettings = []media.FileSetting{
	{Key: media.FileDeleteEmptyFolders, Label: "Delete Empty Folders", Help: "Delete a series or season folder left empty when episode files are deleted."},
	{Key: media.FileUnmonitorDeleted, Label: "Unmonitor Deleted Episodes", Help: "An episode whose file is deleted from disk is unmonitored instead of being fetched again."},
	{Key: media.FileChangeFileDate, Label: "Change File Date", Help: "Change the file date on import and rescan.", Options: []media.Choice{{Key: "none", Label: "None"}, {Key: "release_date", Label: "Episode Air Date"}}},
	{Key: media.FileImportExtraFiles, Label: "Import Extra Files", Help: "Bring files named after an episode along with it, such as its subtitles, under the episode's new name."},
	{Key: media.FileExtraFileExtensions, Label: "Extra File Extensions", Help: "Comma separated, such as srt,ass. A subtitle keeps what follows the video's name (.en.srt)."},
	{Key: media.FileMediaServerFiles, Label: "Jellyfin and Kodi files", Help: "Write what Jellyfin and Kodi read beside the series: tvshow.nfo, season.nfo and an nfo per episode with the TheTVDB ids, and the series' poster, fanart, banner and logo. Only where none exists, never on a read-only root; daily, and at once when switched on."},
	{Key: media.FileMonitorNewGroups, Label: "Monitor new seasons", Help: "When a refresh finds a season the library does not have yet, monitor it if the series already has a monitored season. Off leaves new seasons unmonitored until you choose them."},
	{Key: media.FileSharedFileFallback, Label: "Shared episodes across disks", Help: "An episode listed under two series (a crossover) is held once and linked into the second series' folder. When the two folders are on different disks no link can be made: keep a second copy, or put a symbolic link there.", Options: []media.Choice{{Key: "copy", Label: "Second copy"}, {Key: "symlink", Label: "Symbolic link"}}},
}

// scanMonitoring is what a scan monitors of a series it finds; Sonarr adopts a library as "all", which would hunt every gap.
var scanMonitoring = media.ScanMonitor{Setting: media.ScanMonitorParts, Label: "Monitor series found by a scan", Options: []media.Choice{
	{Key: "existing", Label: "Only the episodes on disk"}, {Key: "all", Label: "Every episode (hunts the gaps)"}, {Key: "none", Label: "None"},
}}

func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:      media.Key(MediaType),
		Label:    "TV",
		Taxonomy: screen.Taxonomy,
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
		ExtraFiles:        media.ExtraFilesDefault{Import: true, Extensions: []string{"srt"}},
		GuessFloor:        GuessFloor,
		Kinds:             libraryKinds,
		FileSettings:      fileSettings,
		ScanMonitor:       scanMonitoring,
		UnidentifiedRetry: RetryScheduleKey,
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
