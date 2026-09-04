package library

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// Subscription is one subject's discography-monitoring rule (ADR-32): "artist
// = person.monitored + subscription, not a desired item -- a rule that
// generates them" (section 19.6). FiltersJSON stays opaque here -- which
// vocabulary it filters on is a media type's own concern (ADR-23), decoded by
// whatever expands it (internal/music's SubscriptionFilters is the first).
type Subscription struct {
	ID                   int64
	SubjectType          string
	SubjectID            int64
	MediaTypes           []string
	Roles                []string
	MonitorBackCatalogue bool
	FiltersJSON          string
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
	}
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
