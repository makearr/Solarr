package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ErrFollowConflict reports a subject already subscribed for another media type: a follow never rewrites that.
var ErrFollowConflict = errors.New("library: already subscribed for another media type")

// FollowRequest is what a follow asks for: where new series go, which roles count, and how far back to reach.
type FollowRequest struct {
	CategoryID int64
	// Roles limits a person's follow to credits in these roles; empty means any.
	Roles         []string
	FiltersJSON   string
	BackCatalogue bool
}

// Follow subscribes the user to a person or an organisation for one media type, or changes the follow they have, and
// marks the subject monitored. Idempotent on (subject, user); a subscription for another media type is refused.
func (s Subscriptions) Follow(ctx context.Context, at time.Time, subjectType string, subjectID, userID int64, mediaType string, request FollowRequest) (int64, error) {
	mediaTypes, err := json.Marshal([]string{mediaType})
	if err != nil {
		return 0, fmt.Errorf("library: follow: %w", err)
	}
	var roles sql.NullString
	if len(request.Roles) > 0 {
		encoded, err := json.Marshal(request.Roles)
		if err != nil {
			return 0, fmt.Errorf("library: follow: %w", err)
		}
		roles = sql.NullString{String: string(encoded), Valid: true}
	}
	user := sql.NullInt64{Int64: userID, Valid: userID != 0}
	category := sql.NullInt64{Int64: request.CategoryID, Valid: request.CategoryID != 0}
	filters := sql.NullString{String: request.FiltersJSON, Valid: request.FiltersJSON != ""}
	back := boolInt(request.BackCatalogue)

	var id int64
	err = s.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		existing, err := queries.GetSubscriptionForSubject(ctx, librarydb.GetSubscriptionForSubjectParams{SubjectType: subjectType, SubjectID: subjectID, UserID: user})
		switch {
		case err == nil:
			if have := subscriptionFrom(existing); !slices.Equal(have.MediaTypes, []string{mediaType}) {
				return ErrFollowConflict
			}
			id = existing.ID
			if err := queries.UpdateSubscriptionScope(ctx, librarydb.UpdateSubscriptionScopeParams{
				MediaTypesJson: string(mediaTypes), RolesJson: roles, CategoryID: category, FiltersJson: filters, MonitorBackCatalogue: back, ID: id,
			}); err != nil {
				return err
			}
		case errors.Is(err, sql.ErrNoRows):
			created, err := queries.CreateSubscription(ctx, librarydb.CreateSubscriptionParams{
				SubjectType: subjectType, SubjectID: subjectID, MediaTypesJson: string(mediaTypes), RolesJson: roles, CategoryID: category,
				FiltersJson: filters, MonitorBackCatalogue: back, Enabled: 1, UserID: user, CreatedAt: at.Unix(),
			})
			if err != nil {
				return err
			}
			id = created.ID
		default:
			return err
		}
		return setSubjectMonitored(ctx, queries, subjectType, subjectID, 1)
	})
	if err != nil {
		return 0, fmt.Errorf("library: follow %s %d: %w", subjectType, subjectID, err)
	}
	return id, nil
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

// Unfollow removes the user's follow of a subject and unmonitors the subject. It never touches series already added.
func (s Subscriptions) Unfollow(ctx context.Context, subjectType string, subjectID, userID int64) error {
	user := sql.NullInt64{Int64: userID, Valid: userID != 0}
	err := s.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		existing, err := queries.GetSubscriptionForSubject(ctx, librarydb.GetSubscriptionForSubjectParams{SubjectType: subjectType, SubjectID: subjectID, UserID: user})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := queries.DeleteSubscription(ctx, existing.ID); err != nil {
			return err
		}
		return setSubjectMonitored(ctx, queries, subjectType, subjectID, 0)
	})
	if err != nil {
		return fmt.Errorf("library: unfollow %s %d: %w", subjectType, subjectID, err)
	}
	return nil
}

// FollowOf answers the user's subscription to a subject, when there is one.
func (s Subscriptions) FollowOf(ctx context.Context, subjectType string, subjectID, userID int64) (Subscription, bool, error) {
	row, err := librarydb.New(s.store.Read()).GetSubscriptionForSubject(ctx, librarydb.GetSubscriptionForSubjectParams{
		SubjectType: subjectType, SubjectID: subjectID, UserID: sql.NullInt64{Int64: userID, Valid: userID != 0},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Subscription{}, false, nil
	}
	if err != nil {
		return Subscription{}, false, fmt.Errorf("library: follow of %s %d: %w", subjectType, subjectID, err)
	}
	return subscriptionFrom(row), true, nil
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
