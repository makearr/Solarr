package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Subject is who or what a follow is of.
type Subject struct {
	// Type is "person" or "organisation".
	Type string
	ID   int64
}

// FollowRequest is what a follow asks for: the library new works go to, which roles count, the media type's own
// options, and whether to reach into the back catalogue.
type FollowRequest struct {
	CategoryID int64
	MediaType  string
	// Roles limits a person's follow to credits in these roles; empty means any.
	Roles         []string
	FiltersJSON   string
	BackCatalogue bool
}

// Follow subscribes the user to a subject in one library, or changes the follow they have there, and marks the
// subject monitored. Idempotent on (subject, user, library).
func (s Subscriptions) Follow(ctx context.Context, at time.Time, subject Subject, userID int64, request FollowRequest) (Subscription, error) {
	var followed Subscription
	err := s.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		followed, err = upsertFollow(ctx, librarydb.New(tx), at, subject, userID, request, true)
		return err
	})
	if err != nil {
		return Subscription{}, fmt.Errorf("library: follow %s %d: %w", subject.Type, subject.ID, err)
	}
	return followed, nil
}

// upsertFollow writes the follow; an existing one is changed only when replace is set, else answered as it is.
func upsertFollow(
	ctx context.Context, queries *librarydb.Queries, at time.Time, subject Subject, userID int64, request FollowRequest, replace bool,
) (Subscription, error) {
	mediaTypes, err := json.Marshal([]string{request.MediaType})
	if err != nil {
		return Subscription{}, err
	}
	var roles sql.NullString
	if len(request.Roles) > 0 {
		encoded, err := json.Marshal(request.Roles)
		if err != nil {
			return Subscription{}, err
		}
		roles = sql.NullString{String: string(encoded), Valid: true}
	}
	user := sql.NullInt64{Int64: userID, Valid: userID != 0}
	category := sql.NullInt64{Int64: request.CategoryID, Valid: request.CategoryID != 0}
	filters := sql.NullString{String: request.FiltersJSON, Valid: request.FiltersJSON != ""}
	back := boolInt(request.BackCatalogue)

	existing, err := queries.GetSubscriptionForSubject(ctx, librarydb.GetSubscriptionForSubjectParams{
		SubjectType: subject.Type, SubjectID: subject.ID, UserID: user, CategoryID: category,
	})
	switch {
	case err == nil && !replace:
		return subscriptionFrom(existing), nil
	case err == nil:
		if err := queries.UpdateSubscriptionScope(ctx, librarydb.UpdateSubscriptionScopeParams{
			MediaTypesJson: string(mediaTypes), RolesJson: roles, CategoryID: category, FiltersJson: filters, MonitorBackCatalogue: back, ID: existing.ID,
		}); err != nil {
			return Subscription{}, err
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := queries.CreateSubscription(ctx, librarydb.CreateSubscriptionParams{
			SubjectType: subject.Type, SubjectID: subject.ID, MediaTypesJson: string(mediaTypes), RolesJson: roles, CategoryID: category,
			FiltersJson: filters, MonitorBackCatalogue: back, Enabled: 1, UserID: user, CreatedAt: at.Unix(),
		}); err != nil {
			return Subscription{}, err
		}
	default:
		return Subscription{}, err
	}
	if err := setSubjectMonitored(ctx, queries, subject.Type, subject.ID, 1); err != nil {
		return Subscription{}, err
	}
	row, err := queries.GetSubscriptionForSubject(ctx, librarydb.GetSubscriptionForSubjectParams{
		SubjectType: subject.Type, SubjectID: subject.ID, UserID: user, CategoryID: category,
	})
	if err != nil {
		return Subscription{}, err
	}
	return subscriptionFrom(row), nil
}

func setSubjectMonitored(ctx context.Context, queries *librarydb.Queries, subjectType string, subjectID, monitored int64) error {
	switch subjectType {
	case "person":
		return queries.SetPersonMonitored(ctx, librarydb.SetPersonMonitoredParams{Monitored: monitored, ID: subjectID})
	case "organisation":
		return queries.SetOrganisationMonitored(ctx, librarydb.SetOrganisationMonitoredParams{Monitored: monitored, ID: subjectID})
	}
	return nil
}

// Unfollow removes the user's follow of a subject in one library; the subject stays monitored while any follow of
// it remains. It never touches works already added.
func (s Subscriptions) Unfollow(ctx context.Context, subject Subject, userID, categoryID int64) error {
	err := s.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		existing, err := queries.GetSubscriptionForSubject(ctx, librarydb.GetSubscriptionForSubjectParams{
			SubjectType: subject.Type, SubjectID: subject.ID, UserID: sql.NullInt64{Int64: userID, Valid: userID != 0},
			CategoryID: sql.NullInt64{Int64: categoryID, Valid: categoryID != 0},
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := queries.DeleteSubscription(ctx, existing.ID); err != nil {
			return err
		}
		remaining, err := queries.CountEnabledSubscriptionsForSubject(ctx, librarydb.CountEnabledSubscriptionsForSubjectParams{
			SubjectType: subject.Type, SubjectID: subject.ID,
		})
		if err != nil || remaining > 0 {
			return err
		}
		return setSubjectMonitored(ctx, queries, subject.Type, subject.ID, 0)
	})
	if err != nil {
		return fmt.Errorf("library: unfollow %s %d: %w", subject.Type, subject.ID, err)
	}
	return nil
}

// FollowsOf answers the user's follows of a subject, one per library.
func (s Subscriptions) FollowsOf(ctx context.Context, subject Subject, userID int64) ([]Subscription, error) {
	rows, err := librarydb.New(s.store.Read()).ListSubscriptionsForSubject(ctx, librarydb.ListSubscriptionsForSubjectParams{
		SubjectType: subject.Type, SubjectID: subject.ID, UserID: sql.NullInt64{Int64: userID, Valid: userID != 0},
	})
	if err != nil {
		return nil, fmt.Errorf("library: follows of %s %d: %w", subject.Type, subject.ID, err)
	}
	out := make([]Subscription, len(rows))
	for i, row := range rows {
		out[i] = subscriptionFrom(row)
	}
	return out, nil
}

// Get answers one subscription by id.
func (s Subscriptions) Get(ctx context.Context, id int64) (Subscription, bool, error) {
	all, err := s.ListEnabled(ctx)
	if err != nil {
		return Subscription{}, false, err
	}
	for _, subscription := range all {
		if subscription.ID == id {
			return subscription, true, nil
		}
	}
	return Subscription{}, false, nil
}

// OrganisationExternalID returns an organisation's id at one provider, when recorded.
func (s Subscriptions) OrganisationExternalID(ctx context.Context, organisationID int64, provider string) (string, bool, error) {
	rows, err := librarydb.New(s.store.Read()).ListOrganisationExternalIDs(ctx, organisationID)
	if err != nil {
		return "", false, fmt.Errorf("library: ids of organisation %d: %w", organisationID, err)
	}
	for _, row := range rows {
		if row.Provider == provider {
			return row.ExternalID, true, nil
		}
	}
	return "", false, nil
}
