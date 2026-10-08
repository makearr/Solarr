package tv

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

const (
	// newSeriesWindow lets a follow also catch a show that premiered shortly before the follow was made.
	newSeriesWindow = 30 * 24 * time.Hour
	// maxPerFollow bounds what one run adds for one follow, so a prolific actor never floods a library; a
	// back-catalogue follow is allowed more because it asked for it.
	maxPerFollow        = 5
	maxPerBackCatalogue = 25
	regularEpisodes     = 3
)

// ErrNoFollowSource reports TV following without a TMDB key: TMDB is the only source of a person's filmography.
var ErrNoFollowSource = fmt.Errorf("%w: following needs a TMDB key", media.ErrNoFollowSource)

// FollowSource is the TMDB client as following needs it.
type FollowSource interface {
	Configured() bool
	Person(ctx context.Context, id int64) (tmdb.Person, error)
	NewOnNetwork(ctx context.Context, networkID int64, since string) ([]tmdb.Hit, error)
}

// FollowFilters is what a TV follow's filters hold.
type FollowFilters struct {
	// Monitor is the add dialog's monitor option for each series the follow adds; "future" when absent.
	Monitor string `json:"monitor,omitempty"`
}

// Following is TV's part of a follow: the new series of a person or network, found through TMDB.
type Following struct {
	People        library.People
	Subscriptions library.Subscriptions
	Source        FollowSource
	Service       Service
	// DefaultProfileID is used when the library names no profile.
	DefaultProfileID int64
	Categories       interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
	Clock clock.Clock
}

// followOptions are what a TV follow offers: how each series it adds is monitored.
var followOptions = []media.FollowOption{{
	Key: "monitor", Label: "Monitor", Default: []string{string(MonitorFuture)},
	Choices: []media.Choice{
		{Key: string(MonitorFuture), Label: "Future episodes"}, {Key: string(MonitorAll), Label: "All episodes"},
		{Key: string(MonitorMissing), Label: "Missing episodes"}, {Key: string(MonitorFirstSeason), Label: "First season"},
		{Key: string(MonitorLastSeason), Label: "Latest season"}, {Key: string(MonitorPilot), Label: "Pilot"},
		{Key: string(MonitorNone), Label: "None"},
	},
}}

// FollowLimit implements media.Following.
func (Following) FollowLimit(backCatalogue bool) int {
	if backCatalogue {
		return maxPerBackCatalogue
	}
	return maxPerFollow
}

// FollowCandidates implements media.Following: the subject's series at TMDB, newest first.
func (f Following) FollowCandidates(ctx context.Context, follow media.Follow) ([]media.FollowCandidate, error) {
	if f.Source == nil || !f.Source.Configured() {
		return nil, ErrNoFollowSource
	}
	since := time.Unix(follow.CreatedAt, 0).UTC().Add(-newSeriesWindow).Format("2006-01-02")
	if follow.BackCatalogue {
		since = ""
	}
	var candidates []media.FollowCandidate
	var err error
	switch follow.SubjectType {
	case "person":
		candidates, err = f.personCandidates(ctx, follow, since)
	case "organisation":
		candidates, err = f.networkCandidates(ctx, follow, since)
	}
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		_, held, err := f.Service.Series.WorkByProviderID(ctx, ProviderTMDB, candidates[i].ExternalID)
		if err != nil {
			return nil, err
		}
		candidates[i].Held = held
	}
	return candidates, nil
}

// AddFollowed implements media.Following: the series is added as the add dialog would, monitored as the follow says.
func (f Following) AddFollowed(ctx context.Context, follow media.Follow, candidate media.FollowCandidate) error {
	id, ok := ParseID(candidate.ExternalID)
	if !ok {
		return fmt.Errorf("tv: follow candidate %q is not a TMDB id", candidate.ExternalID)
	}
	monitor := MonitorFuture
	var filters FollowFilters
	if follow.FiltersJSON != "" && json.Unmarshal([]byte(follow.FiltersJSON), &filters) == nil && MonitorOption(filters.Monitor).Valid() {
		monitor = MonitorOption(filters.Monitor)
	}
	profile := f.DefaultProfileID
	if category, found := f.Categories.Category(follow.CategoryID); found {
		profile = f.Categories.ProfileFor(category, profile)
	}
	_, err := f.Service.Add(ctx, AddRequest{IDs: IDs{TMDB: id}, CategoryID: follow.CategoryID, ProfileID: profile, Monitor: monitor})
	return err
}

func (f Following) personCandidates(ctx context.Context, follow media.Follow, since string) ([]media.FollowCandidate, error) {
	external, found, err := f.People.ExternalIDFor(ctx, follow.SubjectID, ProviderTMDB)
	if err != nil || !found {
		return nil, err
	}
	id, ok := ParseID(external)
	if !ok {
		return nil, nil
	}
	person, err := f.Source.Person(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("tv: follow person %d: %w", id, err)
	}
	wanted := func(role string) bool { return len(follow.Roles) == 0 || slices.Contains(follow.Roles, role) }
	var listed []media.FollowCandidate
	dates := map[string]string{}
	add := func(credit tmdb.Credit, role string) {
		if credit.MediaType != "tv" || !wanted(role) || (since != "" && credit.FirstAirDate < since) {
			return
		}
		if since == "" && role == RoleActor && credit.EpisodeCount < regularEpisodes {
			return
		}
		if credit.FirstAirDate == "" {
			return
		}
		key := strconv.FormatInt(credit.ID, 10)
		dates[key] = credit.FirstAirDate
		listed = append(listed, media.FollowCandidate{
			Provider: ProviderTMDB, ExternalID: key, Title: credit.Name, Year: int64(yearOf(credit.FirstAirDate)),
			PosterURL: tmdb.ImageURL(credit.PosterPath), Role: role,
		})
	}
	for _, credit := range person.CombinedCredits.Cast {
		add(credit, RoleActor)
	}
	for _, credit := range person.CombinedCredits.Crew {
		if role := crewRole(credit.Job); role != "" {
			add(credit, role)
		}
	}
	slices.SortStableFunc(listed, func(a, b media.FollowCandidate) int {
		return -strings.Compare(dates[a.ExternalID], dates[b.ExternalID])
	})
	unique := make([]media.FollowCandidate, 0, len(listed))
	for _, entry := range listed {
		if !slices.ContainsFunc(unique, func(seen media.FollowCandidate) bool { return seen.ExternalID == entry.ExternalID }) {
			unique = append(unique, entry)
		}
	}
	return unique, nil
}

func (f Following) networkCandidates(ctx context.Context, follow media.Follow, since string) ([]media.FollowCandidate, error) {
	external, found, err := f.Subscriptions.OrganisationExternalID(ctx, follow.SubjectID, ProviderTMDB)
	if err != nil || !found {
		return nil, err
	}
	id, ok := ParseID(external)
	if !ok {
		return nil, nil
	}
	if since == "" {
		since = f.Clock.Now().AddDate(-1, 0, 0).Format("2006-01-02")
	}
	hits, err := f.Source.NewOnNetwork(ctx, id, since)
	if err != nil {
		return nil, fmt.Errorf("tv: follow network %d: %w", id, err)
	}
	candidates := make([]media.FollowCandidate, len(hits))
	for i, hit := range hits {
		candidates[i] = media.FollowCandidate{
			Provider: ProviderTMDB, ExternalID: strconv.FormatInt(hit.ID, 10), Title: hit.Name,
			Year: int64(yearOf(hit.FirstAirDate)), PosterURL: tmdb.ImageURL(hit.PosterPath),
		}
	}
	return candidates, nil
}
