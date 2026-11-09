package library

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Reasons a collection is linked with one of another library, or a work sits in one; stored, shown, never parsed.
const (
	LinkReasonProvider    = "provider"
	LinkReasonHand        = "hand"
	MemberReasonCrossover = "crossover"
)

// SiblingProvider is the id namespace of the collection a media type keeps for a franchise id another media type already holds.
func SiblingProvider(provider, mediaType string) string { return provider + "@" + mediaType }

// baseProvider drops the media type a sibling's namespace carries.
func baseProvider(provider string) string {
	base, _, _ := strings.Cut(provider, "@")
	return base
}

// FranchiseKey is the "provider:id" two collections of different media types share when a provider puts them in one franchise.
func FranchiseKey(provider, externalID string) string {
	return baseProvider(provider) + ":" + externalID
}

// ProviderItems are the ids a provider gave among "provider:id" strings, sibling namespaces included.
func ProviderItems(externalIDs []string, provider string) []string {
	var items []string
	for _, id := range externalIDs {
		namespace, item, ok := strings.Cut(id, ":")
		if ok && baseProvider(namespace) == provider {
			items = append(items, item)
		}
	}
	return items
}

// LinkCandidate is a franchise collection as the cross-library rule sees it.
type LinkCandidate struct {
	ID        int64
	MediaType string
	Members   int
	// Keys are every franchise key the collection is known by (its ids and the ones a provider declared for it).
	Keys []string
	// Identity are the collection's own ids as stored, which tell it from a sibling of another media type; a person's undo is kept under them.
	Identity []string
}

// PlannedLink says two collections of different media types are one franchise, and why.
type PlannedLink struct {
	A, B           int64
	Reason, Detail string
}

// PlanCollectionLinks links collections of different media types that share a provider's franchise key, skipping pairs
// already linked and pairs a person unlinked. Collections of one media type never link: the pass that made them settles those.
func PlanCollectionLinks(candidates []LinkCandidate, linked map[[2]int64]bool, refused map[[2]string]bool) []PlannedLink {
	var planned []PlannedLink
	for i, one := range candidates {
		if one.Members == 0 {
			continue
		}
		for _, other := range candidates[i+1:] {
			if other.Members == 0 || other.MediaType == one.MediaType || linked[orderedIDs(one.ID, other.ID)] {
				continue
			}
			if refusedBetween(refused, one.Identity, other.Identity) {
				continue
			}
			shared := ""
			for _, key := range one.Keys {
				if !strings.HasPrefix(key, AutoSource+":") && slices.Contains(other.Keys, key) && (shared == "" || key < shared) {
					shared = key
				}
			}
			if shared != "" {
				planned = append(planned, PlannedLink{A: one.ID, B: other.ID, Reason: LinkReasonProvider, Detail: shared})
			}
		}
	}
	return planned
}

func refusedBetween(refused map[[2]string]bool, a, b []string) bool {
	for _, left := range a {
		for _, right := range b {
			if refused[orderedKeys(left, right)] {
				return true
			}
		}
	}
	return false
}

func orderedIDs(a, b int64) [2]int64 { return [2]int64{min(a, b), max(a, b)} }

func orderedKeys(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// CollectionLink is a link from one collection to another, with why it was made.
type CollectionLink struct {
	OtherID int64
	// Reason is empty for a link made before reasons were kept.
	Reason, Detail string
}

// LinkCandidates lists every franchise collection of every media type with its keys.
func (f Franchises) LinkCandidates(ctx context.Context) ([]LinkCandidate, error) {
	queries := librarydb.New(f.store.Read())
	rows, err := queries.ListLinkableCollections(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: linkable collections: %w", err)
	}
	out := make([]LinkCandidate, len(rows))
	at := make(map[int64]int, len(rows))
	for index, row := range rows {
		out[index] = LinkCandidate{ID: row.ID, MediaType: row.MediaType, Members: int(row.Members)}
		at[row.ID] = index
	}
	ids, err := queries.ListLinkableCollectionIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: linkable collection ids: %w", err)
	}
	for _, row := range ids {
		if index, ok := at[row.CollectionID]; ok {
			out[index].Keys = append(out[index].Keys, FranchiseKey(row.Provider, row.ExternalID))
			out[index].Identity = append(out[index].Identity, row.Provider+":"+row.ExternalID)
		}
	}
	declared, err := queries.ListAllDeclaredKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: declared collection keys: %w", err)
	}
	for _, row := range declared {
		if index, ok := at[row.CollectionID]; ok {
			out[index].Keys = append(out[index].Keys, FranchiseKey(row.Provider, row.ExternalID))
		}
	}
	return out, nil
}

// LinkState answers the links that exist and the pairs of keys a person undid.
func (f Franchises) LinkState(ctx context.Context) (linked map[[2]int64]bool, refused map[[2]string]bool, err error) {
	queries := librarydb.New(f.store.Read())
	rows, err := f.store.Read().QueryContext(ctx,
		`SELECT from_id, to_id FROM relationship WHERE kind = ? AND from_type = 'collection' AND to_type = 'collection'`, CollectionLinkKind)
	if err != nil {
		return nil, nil, fmt.Errorf("library: collection links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	linked = map[[2]int64]bool{}
	for rows.Next() {
		var from, to int64
		if err := rows.Scan(&from, &to); err != nil {
			return nil, nil, fmt.Errorf("library: scan collection link: %w", err)
		}
		linked[orderedIDs(from, to)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("library: collection links: %w", err)
	}
	refusals, err := queries.ListCollectionLinkRefusals(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("library: collection link refusals: %w", err)
	}
	refused = make(map[[2]string]bool, len(refusals))
	for _, row := range refusals {
		refused[[2]string{row.LowKey, row.HighKey}] = true
	}
	return linked, refused, nil
}

// AutoLink links two collections on evidence, recording why; a pair a person unlinked, or that is linked, is left alone.
// It answers whether it linked them.
func (f Franchises) AutoLink(ctx context.Context, link PlannedLink) (bool, error) {
	made := false
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		keysA, err := collectionKeysTx(ctx, queries, link.A)
		if err != nil {
			return err
		}
		keysB, err := collectionKeysTx(ctx, queries, link.B)
		if err != nil {
			return err
		}
		refusals, err := queries.ListCollectionLinkRefusals(ctx)
		if err != nil {
			return err
		}
		refused := make(map[[2]string]bool, len(refusals))
		for _, row := range refusals {
			refused[[2]string{row.LowKey, row.HighKey}] = true
		}
		if refusedBetween(refused, keysA, keysB) {
			return nil
		}
		existing, err := queries.ListCollectionLinks(ctx, link.A)
		if err != nil {
			return err
		}
		if slices.Contains(existing, link.B) {
			return nil
		}
		if err := queries.AddRelationship(ctx, librarydb.AddRelationshipParams{FromType: "collection", FromID: link.A, ToType: "collection", ToID: link.B, Kind: CollectionLinkKind}); err != nil {
			return err
		}
		low, high := orderedIDs(link.A, link.B)[0], orderedIDs(link.A, link.B)[1]
		made = true
		return queries.AddCollectionLinkOrigin(ctx, librarydb.AddCollectionLinkOriginParams{
			LowID: low, HighID: high, Reason: link.Reason, Detail: link.Detail, CreatedAt: f.store.Now().Unix(),
		})
	})
	if err != nil {
		return false, fmt.Errorf("library: auto-link collections %d and %d: %w", link.A, link.B, err)
	}
	return made, nil
}

// collectionKeysTx are a collection's own ids as stored, the identity a person's undo is remembered under.
func collectionKeysTx(ctx context.Context, queries *librarydb.Queries, id int64) ([]string, error) {
	externals, err := queries.ListExternalIDsOfCollection(ctx, id)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(externals))
	for _, external := range externals {
		keys = append(keys, external.Provider+":"+external.ExternalID)
	}
	return keys, nil
}

// refuseLinkTx remembers, under every key pair of the two collections, that a person parted them.
func refuseLinkTx(ctx context.Context, queries *librarydb.Queries, a, b int64) error {
	keysA, err := collectionKeysTx(ctx, queries, a)
	if err != nil {
		return err
	}
	keysB, err := collectionKeysTx(ctx, queries, b)
	if err != nil {
		return err
	}
	for _, left := range keysA {
		for _, right := range keysB {
			pair := orderedKeys(left, right)
			if err := queries.AddCollectionLinkRefusal(ctx, librarydb.AddCollectionLinkRefusalParams{LowKey: pair[0], HighKey: pair[1]}); err != nil {
				return err
			}
		}
	}
	return nil
}

// allowLinkTx forgets that a person parted the two collections, as linking them by hand says they should be shown together.
func allowLinkTx(ctx context.Context, queries *librarydb.Queries, a, b int64) error {
	keysA, err := collectionKeysTx(ctx, queries, a)
	if err != nil {
		return err
	}
	keysB, err := collectionKeysTx(ctx, queries, b)
	if err != nil {
		return err
	}
	for _, left := range keysA {
		for _, right := range keysB {
			pair := orderedKeys(left, right)
			if err := queries.DeleteCollectionLinkRefusal(ctx, librarydb.DeleteCollectionLinkRefusalParams{LowKey: pair[0], HighKey: pair[1]}); err != nil {
				return err
			}
		}
	}
	return nil
}

// DeclareKeys replaces the franchise ids a provider says a collection's works belong to.
func (f Franchises) DeclareKeys(ctx context.Context, collectionID int64, provider string, externalIDs []string) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DeleteCollectionDeclaredKeys(ctx, librarydb.DeleteCollectionDeclaredKeysParams{CollectionID: collectionID, Provider: provider}); err != nil {
			return err
		}
		for _, externalID := range externalIDs {
			if err := queries.AddCollectionDeclaredKey(ctx, librarydb.AddCollectionDeclaredKeyParams{CollectionID: collectionID, Provider: provider, ExternalID: externalID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: declare %s keys of collection %d: %w", provider, collectionID, err)
	}
	return nil
}

// Links lists the collections linked with one, with why each link was made.
func (f Franchises) Links(ctx context.Context, id int64) ([]CollectionLink, error) {
	queries := librarydb.New(f.store.Read())
	others, err := queries.ListCollectionLinks(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("library: links of collection %d: %w", id, err)
	}
	origins, err := queries.ListCollectionLinkOrigins(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("library: link origins of collection %d: %w", id, err)
	}
	out := make([]CollectionLink, len(others))
	for index, other := range others {
		out[index] = CollectionLink{OtherID: other}
		for _, origin := range origins {
			if orderedIDs(id, other) == [2]int64{origin.LowID, origin.HighID} {
				out[index].Reason, out[index].Detail = origin.Reason, origin.Detail
			}
		}
	}
	return out, nil
}

// LinkedCounts are the works the collections linked with one hold, and the total their providers say they have.
type LinkedCounts struct{ Held, Total int }

// LinkedCountsByCollection adds up, per collection, what the collections linked with it hold.
func (f Franchises) LinkedCountsByCollection(ctx context.Context) (map[int64]LinkedCounts, error) {
	rows, err := librarydb.New(f.store.Read()).ListLinkedCollectionCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: linked collection counts: %w", err)
	}
	out := make(map[int64]LinkedCounts)
	for _, row := range rows {
		sum := out[row.ID]
		sum.Held += int(row.OtherHeld)
		sum.Total += int(max(row.OtherTotal, row.OtherHeld))
		out[row.ID] = sum
	}
	return out, nil
}

// MemberOrigin is why Solarr put a work in a collection.
type MemberOrigin struct{ Reason, Detail string }

// RecordMemberOrigin remembers why a work is in a collection Solarr made.
func (f Franchises) RecordMemberOrigin(ctx context.Context, collectionID, workID int64, reason, detail string) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).AddCollectionMemberOrigin(ctx, librarydb.AddCollectionMemberOriginParams{
			CollectionID: collectionID, WorkID: workID, Reason: reason, Detail: detail, CreatedAt: f.store.Now().Unix(),
		})
	})
	if err != nil {
		return fmt.Errorf("library: origin of work %d in collection %d: %w", workID, collectionID, err)
	}
	return nil
}

// MemberOrigins answers why each work of a collection is in it, for those that have a recorded reason.
func (f Franchises) MemberOrigins(ctx context.Context, collectionID int64) (map[int64]MemberOrigin, error) {
	rows, err := librarydb.New(f.store.Read()).ListCollectionMemberOrigins(ctx, collectionID)
	if err != nil {
		return nil, fmt.Errorf("library: member origins of collection %d: %w", collectionID, err)
	}
	out := make(map[int64]MemberOrigin, len(rows))
	for _, row := range rows {
		out[row.WorkID] = MemberOrigin{Reason: row.Reason, Detail: row.Detail}
	}
	return out, nil
}

// PruneCollectionLinks removes links to collections that are gone and links nothing explains, and answers how many.
func (f Franchises) PruneCollectionLinks(ctx context.Context) (int64, error) {
	var pruned int64
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		dangling, err := queries.DeleteDanglingCollectionLinks(ctx)
		if err != nil {
			return err
		}
		unexplained, err := queries.DeleteUnexplainedCollectionLinks(ctx)
		pruned = dangling + unexplained
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("library: prune collection links: %w", err)
	}
	return pruned, nil
}
