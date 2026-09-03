package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// WorkIdentity is what a work should look like if one has to be created. Naming only
// -- persistence is library's job, not a media type's (ADR-23: core carries no
// media-type logic, so this struct is the whole surface a media type gets to shape).
type WorkIdentity struct {
	MediaType      string
	CanonicalTitle string
	SortTitle      string
}

// WorkLink is one caller's Identifier chain result, ready to record: the external id
// it resolved to, and what to create if nothing already claims it.
type WorkLink struct {
	Provider   string
	ExternalID string
	// Confidence is the provider's own confidence in the (Provider, ExternalID)-to-work
	// mapping (ADR-8), not the chain's confidence in having read it correctly.
	Confidence float64
	Work       WorkIdentity
}

// Identity resolves external ids to works (ADR-24's "core owns the chain, a strategy
// only answers for itself" -- persisting what it decided is core's job too).
type Identity struct{ store *store.Store }

// NewIdentity builds it over an open library store.
func NewIdentity(opened *store.Store) Identity { return Identity{store: opened} }

// ResolveWorks finds or creates the work behind each link's (Provider, ExternalID),
// linking it when new, all in one transaction (Invariant 2). A link naming a pair
// already resolved earlier in the same batch reuses that work rather than creating a
// second one: writes are visible to reads within the same transaction.
func (i Identity) ResolveWorks(ctx context.Context, at time.Time, links []WorkLink) error {
	if len(links) == 0 {
		return nil
	}
	return i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, link := range links {
			if _, _, err := resolveWork(ctx, queries, at, link); err != nil {
				return fmt.Errorf("library: resolve work %s:%s: %w", link.Provider, link.ExternalID, err)
			}
		}
		return nil
	})
}

func resolveWork(
	ctx context.Context, queries *librarydb.Queries, at time.Time, link WorkLink,
) (workID int64, created bool, err error) {
	existing, err := queries.GetWorkByExternalID(ctx, librarydb.GetWorkByExternalIDParams{
		Provider: link.Provider, ExternalID: link.ExternalID,
	})
	if err == nil {
		return existing.ID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("look up: %w", err)
	}

	work, err := queries.CreateWork(ctx, librarydb.CreateWorkParams{
		MediaType: link.Work.MediaType, CanonicalTitle: link.Work.CanonicalTitle,
		SortTitle: link.Work.SortTitle, AddedAt: at.Unix(), UpdatedAt: at.Unix(),
	})
	if err != nil {
		return 0, false, fmt.Errorf("create work: %w", err)
	}
	if err := queries.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
		Provider: link.Provider, ExternalID: link.ExternalID, WorkID: work.ID,
		Confidence: link.Confidence, FirstSeenAt: at.Unix(),
	}); err != nil {
		return 0, false, fmt.Errorf("link external id: %w", err)
	}
	return work.ID, true, nil
}
