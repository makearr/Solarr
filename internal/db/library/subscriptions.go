package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Subscription is one subject's discography-monitoring rule (ADR-32): "artist
// = person.monitored + subscription, not a desired item -- a rule that
// generates them" (section 19.6). FiltersJSON stays opaque here -- which
// vocabulary it filters on is a media type's own concern (ADR-23), decoded by
// whatever expands it (internal/media/music's SubscriptionFilters is the first).
type Subscription struct {
	ID                   int64
	SubjectType          string
	SubjectID            int64
	MediaTypes           []string
	Roles                []string
	MonitorBackCatalogue bool
	FiltersJSON          string
	// CategoryID is the library the subscription was created in, 0 when it
	// predates that being recorded.
	CategoryID int64
}

// Subscriptions is subscription CRUD's read side -- People.Subscribe already
// owns the write side (ADR-32's "adding a performer monitors them" in one
// transaction).
type Subscriptions struct{ store *store.Store }

// NewSubscriptions builds it over an open library store.
func NewSubscriptions(opened *store.Store) Subscriptions { return Subscriptions{store: opened} }

// ListEnabled returns every enabled subscription -- what subscription.expand
// walks each run.
func (s Subscriptions) ListEnabled(ctx context.Context) ([]Subscription, error) {
	rows, err := librarydb.New(s.store.Read()).ListEnabledSubscriptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list enabled subscriptions: %w", err)
	}
	out := make([]Subscription, len(rows))
	for index, row := range rows {
		out[index] = subscriptionFrom(row)
	}
	return out, nil
}

func subscriptionFrom(row librarydb.Subscription) Subscription {
	var mediaTypes []string
	_ = json.Unmarshal([]byte(row.MediaTypesJson), &mediaTypes)
	var roles []string
	if row.RolesJson.Valid {
		_ = json.Unmarshal([]byte(row.RolesJson.String), &roles)
	}
	return Subscription{
		ID: row.ID, SubjectType: row.SubjectType, SubjectID: row.SubjectID,
		MediaTypes: mediaTypes, Roles: roles,
		MonitorBackCatalogue: row.MonitorBackCatalogue == 1,
		FiltersJSON:          row.FiltersJson.String,
		CategoryID:           row.CategoryID.Int64,
	}
}

// Find answers the subscription for one subject and user, without creating
// one -- the same "did this call create it or only match it" question
// People.Subscribe's own idempotent return value cannot answer on its own,
// needed by a caller that must record which is which (todo 4.13's
// ArrImportRevert).
func (s Subscriptions) Find(ctx context.Context, subjectType string, subjectID, userID int64) (int64, bool, error) {
	row, err := librarydb.New(s.store.Read()).GetSubscriptionForSubject(ctx, librarydb.GetSubscriptionForSubjectParams{
		SubjectType: subjectType, SubjectID: subjectID, UserID: sql.NullInt64{Int64: userID, Valid: userID != 0},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("library: find subscription for %s %d: %w", subjectType, subjectID, err)
	}
	return row.ID, true, nil
}

// Delete removes a subscription outright. Revert-only use so far (todo
// 4.13's ArrImportRevert): must run before People.Delete for the same
// person, since subscription.subject_id carries no FK/cascade of its own to
// clean up after a deleted person.
func (s Subscriptions) Delete(ctx context.Context, subscriptionID int64) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteSubscription(ctx, subscriptionID)
	})
	if err != nil {
		return fmt.Errorf("library: delete subscription %d: %w", subscriptionID, err)
	}
	return nil
}

// ExternalIDFor returns a person's external id for one provider (e.g.
// "musicbrainz:artist"), when one is recorded -- what subscription.expand
// needs to go from a subscribed person back to the id their discography is
// browsed by.
func (p People) ExternalIDFor(ctx context.Context, personID int64, provider string) (string, bool, error) {
	rows, err := librarydb.New(p.store.Read()).ListPersonExternalIDs(ctx, personID)
	if err != nil {
		return "", false, fmt.Errorf("library: external id for person %d: %w", personID, err)
	}
	for _, row := range rows {
		if row.Provider == provider {
			return row.ExternalID, true, nil
		}
	}
	return "", false, nil
}
