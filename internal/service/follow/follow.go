// Package follow is following a person or organisation into a library, for every media type (ADR-32).
package follow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNotFollowable reports a library whose media type has nothing a follow could add.
var ErrNotFollowable = errors.New("follow: this library's media type cannot follow")

// ErrNoLibrary reports a follow naming a library that does not exist.
var ErrNoLibrary = errors.New("follow: no such library")

// JobType is the sweep that adds the new works of every follow.
const JobType = jobs.Type("follow.expand")

// actionKey deduplicates the sweep, and with a subscription id one follow's own run (Invariant 16).
const actionKey = "follow.expand"

// Definition declares the sweep: background metadata work that adds works, retried on transient failure.
var Definition = jobs.Definition{
	Name: "Adding the works of followed people", Type: JobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// Libraries answers a library by id.
type Libraries interface {
	Category(id int64) (settings.Category, bool)
}

// Service follows, unfollows and previews, and is the sweep's handler.
type Service struct {
	Subscriptions library.Subscriptions
	Exclusions    library.Exclusions
	Media         *media.Registry
	Libraries     Libraries
	// Queue runs a new follow at once; nil leaves it to the schedule.
	Queue  *jobs.Queue
	Clock  clock.Clock
	Logger *slog.Logger
}

// Request is what a follow asks for in one library.
type Request struct {
	CategoryID int64
	// Roles limits a person's follow to these roles; empty is any.
	Roles []string
	// Options are the media type's follow options by key, a missing key taking its default.
	Options       map[string][]string
	BackCatalogue bool
	// Search looks for what the follow adds as soon as it is added.
	Search bool
}

// Preview is what a follow would do right now, so it can be seen before it is turned on (ADR-38).
type Preview struct {
	// WouldAdd is what the next run adds, at most Limit when there is one.
	WouldAdd []media.FollowCandidate
	// More counts candidates beyond the limit, added by later runs.
	More int
	// Held counts candidates the library already has or has excluded.
	Held  int
	Limit int
}

// Payload scopes a run to one follow, the one just made; empty is the sweep.
type Payload struct {
	SubscriptionID int64 `json:"subscription_id,omitempty"`
	Search         bool  `json:"search,omitempty"`
}

func (s Service) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

// following answers the library's media type as a following module.
func (s Service) following(categoryID int64) (media.Following, settings.Category, error) {
	category, ok := s.Libraries.Category(categoryID)
	if !ok {
		return nil, settings.Category{}, ErrNoLibrary
	}
	module, ok := media.Capability[media.Following](s.Media, media.Key(category.MediaType))
	if !ok {
		return nil, category, ErrNotFollowable
	}
	return module, category, nil
}

// filtersJSON stores the options the media type declares, the rest dropped.
func (s Service) filtersJSON(mediaType string, options map[string][]string) (string, error) {
	module, ok := s.Media.Module(media.Key(mediaType))
	if !ok || len(options) == 0 {
		return "", nil
	}
	stored := map[string]any{}
	for _, option := range module.Descriptor().FollowOptions {
		values, given := options[option.Key]
		if !given {
			continue
		}
		for _, value := range values {
			if !slices.ContainsFunc(option.Choices, func(choice media.Choice) bool { return choice.Key == value }) {
				return "", fmt.Errorf("%w: %s is not a choice of %s", ErrInvalidOption, value, option.Key)
			}
		}
		if option.Many {
			stored[option.Key] = values
		} else if len(values) > 0 {
			stored[option.Key] = values[0]
		}
	}
	if len(stored) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(stored)
	return string(encoded), err
}

// ErrInvalidOption reports a follow option the media type does not offer.
var ErrInvalidOption = errors.New("follow: invalid option")

// Preview answers what following the subject into the request's library would add, without following.
func (s Service) Preview(ctx context.Context, subject library.Subject, request Request) (Preview, error) {
	module, category, err := s.following(request.CategoryID)
	if err != nil {
		return Preview{}, err
	}
	filters, err := s.filtersJSON(category.MediaType, request.Options)
	if err != nil {
		return Preview{}, err
	}
	follow := media.Follow{
		SubjectType: subject.Type, SubjectID: subject.ID, CategoryID: category.ID, Roles: request.Roles,
		FiltersJSON: filters, BackCatalogue: request.BackCatalogue, CreatedAt: s.Clock.Now().Unix(),
	}
	candidates, err := module.FollowCandidates(ctx, follow)
	if err != nil {
		return Preview{}, err
	}
	preview := Preview{Limit: module.FollowLimit(request.BackCatalogue)}
	for _, candidate := range candidates {
		skip, err := s.skipped(ctx, candidate)
		if err != nil {
			return Preview{}, err
		}
		switch {
		case skip:
			preview.Held++
		case preview.Limit == 0 || len(preview.WouldAdd) < preview.Limit:
			preview.WouldAdd = append(preview.WouldAdd, candidate)
		default:
			preview.More++
		}
	}
	return preview, nil
}

// skipped reports a candidate the library holds or the user excluded.
func (s Service) skipped(ctx context.Context, candidate media.FollowCandidate) (bool, error) {
	if candidate.Held {
		return true, nil
	}
	return s.Exclusions.Excluded(ctx, []library.ExternalIDWrite{{Provider: candidate.Provider, ExternalID: candidate.ExternalID}})
}

// Follow follows the subject into the request's library, or changes the follow there, and runs it at once.
func (s Service) Follow(ctx context.Context, subject library.Subject, userID int64, request Request) (library.Subscription, error) {
	_, category, err := s.following(request.CategoryID)
	if err != nil {
		return library.Subscription{}, err
	}
	filters, err := s.filtersJSON(category.MediaType, request.Options)
	if err != nil {
		return library.Subscription{}, err
	}
	followed, err := s.Subscriptions.Follow(ctx, s.Clock.Now(), subject, userID, library.FollowRequest{
		CategoryID: category.ID, MediaType: category.MediaType, Roles: request.Roles, FiltersJSON: filters,
		BackCatalogue: request.BackCatalogue,
	})
	if err != nil {
		return library.Subscription{}, err
	}
	if s.Queue != nil {
		if _, err := Enqueue(ctx, s.Queue, followed.ID, request.Search); err != nil {
			s.logger().WarnContext(ctx, "follow: run the new follow", slog.Any("error", err))
		}
	}
	return followed, nil
}

// Unfollow ends the user's follow of the subject in one library; what it added stays.
func (s Service) Unfollow(ctx context.Context, subject library.Subject, userID, categoryID int64) error {
	return s.Subscriptions.Unfollow(ctx, subject, userID, categoryID)
}

// Follows answers the user's follows of the subject, one per library.
func (s Service) Follows(ctx context.Context, subject library.Subject, userID int64) ([]library.Subscription, error) {
	return s.Subscriptions.FollowsOf(ctx, subject, userID)
}

// Run is the sweep: every enabled follow, or the one the payload names; one follow failing never stops the others.
func (s Service) Run(ctx context.Context, job jobs.Job) error {
	var payload Payload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Terminalf("follow.payload", string(JobType), err)
		}
	}
	subscriptions, err := s.Subscriptions.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("follow: sweep: %w", err)
	}
	added, failed := 0, 0
	for _, subscription := range subscriptions {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("follow.context", string(JobType), err)
		}
		if payload.SubscriptionID != 0 && subscription.ID != payload.SubscriptionID {
			continue
		}
		count, err := s.expand(ctx, subscription, payload.Search)
		added += count
		if err != nil {
			failed++
			s.logger().WarnContext(ctx, "follow failed", slog.Int64("subscription_id", subscription.ID), slog.Any("error", err))
		}
	}
	s.logger().Info("follow sweep finished", slog.Int("added", added), slog.Int("failed", failed))
	return nil
}

// expand adds what one follow admits and the library lacks, up to the media type's limit.
func (s Service) expand(ctx context.Context, subscription library.Subscription, search bool) (int, error) {
	module, ok := s.moduleOf(subscription)
	if !ok {
		return 0, nil
	}
	follow := media.Follow{
		SubjectType: subscription.SubjectType, SubjectID: subscription.SubjectID, CategoryID: subscription.CategoryID,
		Roles: subscription.Roles, FiltersJSON: subscription.FiltersJSON, BackCatalogue: subscription.MonitorBackCatalogue,
		CreatedAt: subscription.CreatedAt,
	}
	candidates, err := module.FollowCandidates(ctx, follow)
	if err != nil {
		return 0, err
	}
	limit := module.FollowLimit(subscription.MonitorBackCatalogue)
	added := 0
	for _, candidate := range candidates {
		if limit > 0 && added == limit {
			break
		}
		skip, err := s.skipped(ctx, candidate)
		if err != nil {
			return added, err
		}
		if skip {
			continue
		}
		if err := module.AddFollowed(ctx, follow, candidate); err != nil {
			s.logger().WarnContext(ctx, "follow could not add a work", slog.String("provider", candidate.Provider),
				slog.String("id", candidate.ExternalID), slog.Any("error", err))
			continue
		}
		added++
	}
	if search {
		if searching, ok := module.(media.FollowSearching); ok {
			if err := searching.SearchFollowed(ctx, follow); err != nil {
				return added, fmt.Errorf("search what was added: %w", err)
			}
		}
	}
	return added, nil
}

// moduleOf answers the following module of a follow: its library's media type, else the one it was made for.
func (s Service) moduleOf(subscription library.Subscription) (media.Following, bool) {
	mediaType := ""
	if category, ok := s.Libraries.Category(subscription.CategoryID); ok {
		mediaType = category.MediaType
	} else if len(subscription.MediaTypes) > 0 {
		mediaType = subscription.MediaTypes[0]
	}
	return media.Capability[media.Following](s.Media, media.Key(mediaType))
}

// Enqueue submits the sweep, or one follow's own run when subscriptionID is set.
func Enqueue(ctx context.Context, queue *jobs.Queue, subscriptionID int64, search bool) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(Payload{SubscriptionID: subscriptionID, Search: search})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("follow: payload: %w", err)
	}
	key, priority := actionKey, jobs.PriorityLow
	if subscriptionID != 0 {
		key, priority = actionKey+":"+strconv.FormatInt(subscriptionID, 10), jobs.PriorityNormal
	}
	return queue.Enqueue(ctx, jobs.Request{Type: JobType, ActionKey: key, Priority: priority, Payload: payload})
}
