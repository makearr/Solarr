package tv

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// FollowJobType is the registered type of the sweep that adds the new series of followed people and networks.
const FollowJobType = jobs.Type("tv.follow_expand")

// FollowActionKey deduplicates the sweep: one at a time, like every expansion (Invariant 16).
const FollowActionKey = "tv.follow_expand"

// FollowDefinition declares the sweep: background metadata work that adds series, retried on transient failure.
var FollowDefinition = jobs.Definition{
	Name: "Adding the new series of followed people and networks",
	Type: FollowJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

const (
	// newSeriesWindow lets a follow also catch a show that premiered shortly before the follow was made.
	newSeriesWindow = 30 * 24 * time.Hour
	// maxPerFollow bounds what one run adds for one follow, so a prolific actor never floods a library; a
	// back-catalogue follow is allowed more because it asked for it.
	maxPerFollow        = 5
	maxPerBackCatalogue = 25
	regularEpisodes     = 3
)

// FollowSource is the TMDB client as the follow sweep needs it.
type FollowSource interface {
	Configured() bool
	Person(ctx context.Context, id int64) (tmdb.Person, error)
	NewOnNetwork(ctx context.Context, networkID int64, since string) ([]tmdb.Hit, error)
}

// FollowPayload scopes a run to one subscription, the follow just made; empty is the sweep.
type FollowPayload struct {
	SubscriptionID int64 `json:"subscription_id,omitempty"`
}

// FollowFilters is what a follow's filters_json holds for TV.
type FollowFilters struct {
	// Monitor is the add dialog's monitor option for each series the follow adds; "future" when absent.
	Monitor string `json:"monitor,omitempty"`
}

// FollowConfig is what a FollowJob needs; Logger is optional.
type FollowConfig struct {
	Subscriptions    library.Subscriptions
	People           library.People
	Source           FollowSource
	Service          Service
	DefaultProfileID int64
	Categories       interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
	Clock  clock.Clock
	Logger *slog.Logger
}

// FollowJob adds the new series of every followed person and network. Additive only: a series already held, even
// unmonitored, is left exactly as it is (Invariant 71).
type FollowJob struct {
	config FollowConfig
	logger *slog.Logger
}

// NewFollowJob builds the handler.
func NewFollowJob(config FollowConfig) *FollowJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &FollowJob{config: config, logger: logger}
}

// Run walks the enabled TV follows once; one follow failing never stops the others.
func (j *FollowJob) Run(ctx context.Context, job jobs.Job) error {
	var payload FollowPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Terminalf("tv.follow.payload", string(FollowJobType), err)
		}
	}
	if j.config.Source == nil || !j.config.Source.Configured() {
		return nil
	}
	subscriptions, err := j.config.Subscriptions.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("tv: follow sweep: %w", err)
	}
	added, failed := 0, 0
	for _, subscription := range subscriptions {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("tv.follow.context", string(FollowJobType), err)
		}
		if !slices.Contains(subscription.MediaTypes, MediaType) || (payload.SubscriptionID != 0 && subscription.ID != payload.SubscriptionID) {
			continue
		}
		count, err := j.follow(ctx, subscription)
		added += count
		if err != nil {
			failed++
			j.logger.WarnContext(ctx, "tv follow failed", slog.Int64("subscription_id", subscription.ID), slog.Any("error", err))
		}
	}
	j.logger.Info("tv follow sweep finished", slog.Int("added", added), slog.Int("failed", failed))
	return nil
}

func (j *FollowJob) follow(ctx context.Context, subscription library.Subscription) (int, error) {
	if subscription.CategoryID == 0 {
		return 0, nil
	}
	candidates, err := j.candidates(ctx, subscription)
	if err != nil {
		return 0, err
	}
	limit := maxPerFollow
	if subscription.MonitorBackCatalogue {
		limit = maxPerBackCatalogue
	}
	added := 0
	for _, candidate := range candidates {
		if added == limit {
			break
		}
		held, err := j.held(ctx, candidate.TMDBID)
		if err != nil || held || j.config.Service.IsExcluded(ctx, IDs{TMDB: candidate.TMDBID}) {
			continue
		}
		if err := j.add(ctx, subscription, candidate.TMDBID); err != nil {
			j.logger.WarnContext(ctx, "tv follow could not add a series", slog.Int64("tmdb", candidate.TMDBID), slog.Any("error", err))
			continue
		}
		added++
	}
	return added, nil
}

// FollowCandidate is a series a follow would add.
type FollowCandidate struct {
	TMDBID    int64
	Title     string
	Year      int64
	PosterURL string
	// Role is what the person did on it; empty for a network.
	Role string
}

// FollowPreview is what a follow would do right now, so the user can see it before turning the follow on.
type FollowPreview struct {
	// WouldAdd is what the next run adds, at most Limit.
	WouldAdd []FollowCandidate
	// More counts candidates beyond the limit, added by later runs.
	More int
	// Held counts candidates the library already has or has excluded.
	Held  int
	Limit int
}

// Preview lists what the follow would add, without adding anything or storing the follow.
func (j *FollowJob) Preview(ctx context.Context, subscription library.Subscription) (FollowPreview, error) {
	if j.config.Source == nil || !j.config.Source.Configured() {
		return FollowPreview{}, nil
	}
	candidates, err := j.candidates(ctx, subscription)
	if err != nil {
		return FollowPreview{}, err
	}
	preview := FollowPreview{Limit: maxPerFollow}
	if subscription.MonitorBackCatalogue {
		preview.Limit = maxPerBackCatalogue
	}
	for _, candidate := range candidates {
		held, err := j.held(ctx, candidate.TMDBID)
		if err != nil {
			return FollowPreview{}, err
		}
		switch {
		case held || j.config.Service.IsExcluded(ctx, IDs{TMDB: candidate.TMDBID}):
			preview.Held++
		case len(preview.WouldAdd) < preview.Limit:
			preview.WouldAdd = append(preview.WouldAdd, candidate)
		default:
			preview.More++
		}
	}
	return preview, nil
}

func (j *FollowJob) held(ctx context.Context, tmdbID int64) (bool, error) {
	_, held, err := j.config.Service.Series.WorkByProviderID(ctx, ProviderTMDB, strconv.FormatInt(tmdbID, 10))
	return held, err
}

func (j *FollowJob) add(ctx context.Context, subscription library.Subscription, tmdbID int64) error {
	monitor := MonitorFuture
	var filters FollowFilters
	if subscription.FiltersJSON != "" && json.Unmarshal([]byte(subscription.FiltersJSON), &filters) == nil && MonitorOption(filters.Monitor).Valid() {
		monitor = MonitorOption(filters.Monitor)
	}
	profile := j.config.DefaultProfileID
	if category, found := j.config.Categories.Category(subscription.CategoryID); found {
		profile = j.config.Categories.ProfileFor(category, profile)
	}
	_, err := j.config.Service.Add(ctx, AddRequest{IDs: IDs{TMDB: tmdbID}, CategoryID: subscription.CategoryID, ProfileID: profile, Monitor: monitor})
	return err
}

// candidates lists the TMDB ids of series the follow would add, newest first, before any are checked against the library.
func (j *FollowJob) candidates(ctx context.Context, subscription library.Subscription) ([]FollowCandidate, error) {
	since := time.Unix(subscription.CreatedAt, 0).UTC().Add(-newSeriesWindow).Format("2006-01-02")
	if subscription.MonitorBackCatalogue {
		since = ""
	}
	switch subscription.SubjectType {
	case "person":
		return j.personCandidates(ctx, subscription, since)
	case "organisation":
		return j.networkCandidates(ctx, subscription, since)
	}
	return nil, nil
}

func (j *FollowJob) personCandidates(ctx context.Context, subscription library.Subscription, since string) ([]FollowCandidate, error) {
	external, found, err := j.config.People.ExternalIDFor(ctx, subscription.SubjectID, ProviderTMDB)
	if err != nil || !found {
		return nil, err
	}
	id, ok := ParseID(external)
	if !ok {
		return nil, nil
	}
	person, err := j.config.Source.Person(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("tv: follow person %d: %w", id, err)
	}
	wanted := func(role string) bool {
		return len(subscription.Roles) == 0 || slices.Contains(subscription.Roles, role)
	}
	var listed []FollowCandidate
	dates := map[int64]string{}
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
		dates[credit.ID] = credit.FirstAirDate
		listed = append(listed, FollowCandidate{
			TMDBID: credit.ID, Title: credit.Name, Year: int64(yearOf(credit.FirstAirDate)), PosterURL: tmdb.ImageURL(credit.PosterPath), Role: role,
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
	slices.SortStableFunc(listed, func(a, b FollowCandidate) int { return -strings.Compare(dates[a.TMDBID], dates[b.TMDBID]) })
	unique := make([]FollowCandidate, 0, len(listed))
	for _, entry := range listed {
		if !slices.ContainsFunc(unique, func(seen FollowCandidate) bool { return seen.TMDBID == entry.TMDBID }) {
			unique = append(unique, entry)
		}
	}
	return unique, nil
}

func (j *FollowJob) networkCandidates(ctx context.Context, subscription library.Subscription, since string) ([]FollowCandidate, error) {
	external, found, err := j.config.Subscriptions.OrganisationExternalID(ctx, subscription.SubjectID, ProviderTMDB)
	if err != nil || !found {
		return nil, err
	}
	id, ok := ParseID(external)
	if !ok {
		return nil, nil
	}
	if since == "" {
		since = j.config.Clock.Now().AddDate(-1, 0, 0).Format("2006-01-02")
	}
	hits, err := j.config.Source.NewOnNetwork(ctx, id, since)
	if err != nil {
		return nil, fmt.Errorf("tv: follow network %d: %w", id, err)
	}
	candidates := make([]FollowCandidate, len(hits))
	for i, hit := range hits {
		candidates[i] = FollowCandidate{TMDBID: hit.ID, Title: hit.Name, Year: int64(yearOf(hit.FirstAirDate)), PosterURL: tmdb.ImageURL(hit.PosterPath)}
	}
	return candidates, nil
}

// EnqueueFollow submits one follow sweep, or the one follow named by subscriptionID, deduplicated by FollowActionKey.
func EnqueueFollow(ctx context.Context, queue *jobs.Queue, subscriptionID int64) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(FollowPayload{SubscriptionID: subscriptionID})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("tv: follow payload: %w", err)
	}
	key := FollowActionKey
	if subscriptionID != 0 {
		key += ":" + strconv.FormatInt(subscriptionID, 10)
	}
	return queue.Enqueue(ctx, jobs.Request{Type: FollowJobType, ActionKey: key, Priority: jobs.PriorityLow, Payload: payload})
}
