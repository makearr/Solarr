package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// FranchiseKind is the collection kind of a movie franchise a provider names (TMDB's collections).
const FranchiseKind = "franchise"

// Franchises are the collections of whole works a provider groups: the films of one franchise.
type Franchises struct{ store *store.Store }

// NewFranchises builds it over an open library store.
func NewFranchises(opened *store.Store) Franchises { return Franchises{store: opened} }

// Join puts a work in the franchise a provider names, creating it when new and keeping its name current; a franchise a
// person monitored stays monitored. It answers the collection's id.
func (f Franchises) Join(ctx context.Context, mediaType, provider, externalID, name string, workID int64) (int64, error) {
	var id int64
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		return joinFranchiseTx(ctx, queries, &id, f.store.Now().Unix(), mediaType, provider, externalID, name, workID)
	})
	if err != nil {
		return 0, fmt.Errorf("library: join franchise %s:%s: %w", provider, externalID, err)
	}
	return id, nil
}

func joinFranchiseTx(
	ctx context.Context, queries *librarydb.Queries, id *int64, now int64, mediaType, provider, externalID, name string, workID int64,
) error {
	found, err := queries.GetCollectionByExternalID(ctx, librarydb.GetCollectionByExternalIDParams{Provider: provider, ExternalID: externalID})
	switch {
	case err == nil:
		*id = found.ID
		if name != "" && found.Name != name {
			if err := queries.SetCollectionName(ctx, librarydb.SetCollectionNameParams{Name: name, ID: found.ID}); err != nil {
				return err
			}
		}
	case errors.Is(err, sql.ErrNoRows):
		created, err := queries.CreateCollection(ctx, librarydb.CreateCollectionParams{
			MediaType: mediaType, Name: name, Kind: FranchiseKind, Source: provider, AddedAt: now,
		})
		if err != nil {
			return err
		}
		*id = created.ID
		if err := queries.LinkCollectionExternalID(ctx, librarydb.LinkCollectionExternalIDParams{Provider: provider, ExternalID: externalID, CollectionID: created.ID}); err != nil {
			return err
		}
	default:
		return err
	}
	return queries.AddCollectionMember(ctx, librarydb.AddCollectionMemberParams{CollectionID: *id, WorkID: workID, PartID: 0})
}

// SetMonitoredOf sets the monitored flag of every franchise a work belongs to.
func (f Franchises) SetMonitoredOf(ctx context.Context, workID int64, monitored bool) error {
	err := f.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		collections, err := queries.ListCollectionsForWork(ctx, workID)
		if err != nil {
			return err
		}
		for _, collection := range collections {
			if collection.Kind != FranchiseKind {
				continue
			}
			if err := queries.SetCollectionMonitored(ctx, librarydb.SetCollectionMonitoredParams{Monitored: boolToInt(monitored), ID: collection.ID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: monitor franchises of work %d: %w", workID, err)
	}
	return nil
}

// FranchiseMember is one film of a franchise the library holds.
type FranchiseMember struct {
	WorkID    int64
	Title     string
	Year      int64
	Monitored bool
	Files     int64
	// PosterURL is the work's picture at its provider, empty when it has none.
	PosterURL string
}

// FranchiseRow is a collection as a library holds it: its members, and the member count the provider states.
type FranchiseRow struct {
	ID         int64
	Name       string
	Kind       string
	ExternalID string
	Monitored  bool
	PosterURL  string
	// Total is the works the provider says the franchise has, 0 when unknown.
	Total   int64
	Members []FranchiseMember
}

// CustomKind is the kind of a collection a person made by hand.
const CustomKind = "custom"

// InCategory lists a library's collections with the works of each it holds, by name: the provider's franchises that
// have a work of the library, and the library's own hand-made ones, empty or not.
func (f Franchises) InCategory(ctx context.Context, mediaType string, categoryID int64) ([]FranchiseRow, error) {
	queries := librarydb.New(f.store.Read())
	collections, err := queries.ListCollectionsInCategory(ctx, librarydb.ListCollectionsInCategoryParams{MediaType: mediaType, CategoryID: categoryID})
	if err != nil {
		return nil, fmt.Errorf("library: collections of category %d: %w", categoryID, err)
	}
	out := make([]FranchiseRow, len(collections))
	ids := make([]int64, len(collections))
	at := make(map[int64]int, len(collections))
	for index, row := range collections {
		out[index] = FranchiseRow{ID: row.ID, Name: row.Name, Kind: row.Kind, ExternalID: row.ExternalID, Monitored: row.Monitored == 1, Total: row.Total.Int64, PosterURL: row.PosterUrl}
		ids[index], at[row.ID] = row.ID, index
	}
	if len(ids) == 0 {
		return out, nil
	}
	members, err := queries.ListCollectionMembersInCategory(ctx, librarydb.ListCollectionMembersInCategoryParams{CollectionIds: ids, CategoryID: categoryID})
	if err != nil {
		return nil, fmt.Errorf("library: collection members of category %d: %w", categoryID, err)
	}
	for _, row := range members {
		entry := &out[at[row.CollectionID]]
		entry.Members = append(entry.Members, FranchiseMember{WorkID: row.WorkID, Title: row.Title, Year: row.Year, Monitored: row.Monitored > 0, Files: row.Files, PosterURL: posterOf(row.Images)})
	}
	return out, nil
}

// Create makes an empty collection of the library, by hand, and answers its id.
func (f Franchises) Create(ctx context.Context, mediaType string, categoryID int64, name string) (int64, error) {
	var id int64
	err := f.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		id, err = librarydb.New(tx).CreateCustomCollection(ctx, librarydb.CreateCustomCollectionParams{
			MediaType: mediaType, Name: name, AddedAt: f.store.Now().Unix(), CategoryID: categoryID,
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("library: create collection: %w", err)
	}
	return id, nil
}

// ErrNotCustomCollection is a collection that is not a hand-made one, or none: only those are renamed, deleted or filled by hand.
var ErrNotCustomCollection = errors.New("library: not a collection made by hand")

// Rename renames a hand-made collection.
func (f Franchises) Rename(ctx context.Context, id int64, name string) error {
	return f.customWrite(ctx, id, func(queries *librarydb.Queries) (int64, error) {
		return queries.RenameCustomCollection(ctx, librarydb.RenameCustomCollectionParams{Name: name, ID: id})
	})
}

// Delete removes a hand-made collection, never the works in it.
func (f Franchises) Delete(ctx context.Context, id int64) error {
	return f.customWrite(ctx, id, func(queries *librarydb.Queries) (int64, error) { return queries.DeleteCustomCollection(ctx, id) })
}

// AddMember puts a work in a hand-made collection.
func (f Franchises) AddMember(ctx context.Context, id, workID int64) error {
	return f.customWrite(ctx, id, func(queries *librarydb.Queries) (int64, error) {
		collection, err := queries.GetCollection(ctx, id)
		if err != nil || collection.Kind != CustomKind {
			return 0, err
		}
		return 1, queries.AddCollectionMember(ctx, librarydb.AddCollectionMemberParams{CollectionID: id, WorkID: workID, PartID: 0})
	})
}

// RemoveMember takes a work out of a hand-made collection.
func (f Franchises) RemoveMember(ctx context.Context, id, workID int64) error {
	return f.customWrite(ctx, id, func(queries *librarydb.Queries) (int64, error) {
		collection, err := queries.GetCollection(ctx, id)
		if err != nil || collection.Kind != CustomKind {
			return 0, err
		}
		return 1, queries.RemoveCollectionMember(ctx, librarydb.RemoveCollectionMemberParams{CollectionID: id, WorkID: workID})
	})
}

func (f Franchises) customWrite(ctx context.Context, id int64, write func(*librarydb.Queries) (int64, error)) error {
	var changed int64
	err := f.store.Write(ctx, store.PrioInteractive, func(_ context.Context, tx *sql.Tx) error {
		var err error
		changed, err = write(librarydb.New(tx))
		return err
	})
	if err != nil {
		return fmt.Errorf("library: collection %d: %w", id, err)
	}
	if changed == 0 {
		return ErrNotCustomCollection
	}
	return nil
}

// Get reads one franchise's name, monitoring and provider id; sql.ErrNoRows wrapped when it is none.
func (f Franchises) Get(ctx context.Context, id int64) (FranchiseRow, error) {
	queries := librarydb.New(f.store.Read())
	collection, err := queries.GetCollection(ctx, id)
	if err != nil {
		return FranchiseRow{}, fmt.Errorf("library: franchise %d: %w", id, err)
	}
	row := FranchiseRow{ID: collection.ID, Name: collection.Name, Kind: collection.Kind, Monitored: collection.Monitored == 1, Total: collection.CompleteAt.Int64}
	if external, err := queries.GetCollectionExternalIDOf(ctx, id); err == nil {
		row.ExternalID = external.ExternalID
	}
	return row, nil
}

// SetMonitored sets a franchise's own monitored flag and, when the provider stated one, how many films it has.
func (f Franchises) SetMonitored(ctx context.Context, id int64, monitored bool, total int64) error {
	err := f.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.SetCollectionMonitored(ctx, librarydb.SetCollectionMonitoredParams{Monitored: boolToInt(monitored), ID: id}); err != nil {
			return err
		}
		if total > 0 {
			return queries.SetCollectionComplete(ctx, librarydb.SetCollectionCompleteParams{CompleteAt: sql.NullInt64{Int64: total, Valid: true}, ID: id})
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: monitor franchise %d: %w", id, err)
	}
	return nil
}

// SetPoster keeps the provider's picture of a franchise.
func (f Franchises) SetPoster(ctx context.Context, id int64, posterURL string) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetCollectionPoster(ctx, librarydb.SetCollectionPosterParams{PosterUrl: posterURL, ID: id})
	})
	if err != nil {
		return fmt.Errorf("library: franchise %d poster: %w", id, err)
	}
	return nil
}

// UncheckedSeries is a series no franchise lookup has been made for, with the ids a lookup goes by.
type UncheckedSeries struct {
	WorkID     int64
	TVDB, TMDB int64
	// AniList is the series' AniList id, 0 when it has none.
	AniList int64
}

// SeriesWithoutFranchiseCheck lists up to limit series the franchise pass has not asked about yet.
func (f Franchises) SeriesWithoutFranchiseCheck(ctx context.Context, limit int64) ([]UncheckedSeries, error) {
	rows, err := librarydb.New(f.store.Read()).ListSeriesWithoutFranchiseCheck(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("library: series without a franchise check: %w", err)
	}
	out := make([]UncheckedSeries, len(rows))
	for index, row := range rows {
		out[index] = UncheckedSeries{WorkID: row.WorkID}
		out[index].TVDB, _ = strconv.ParseInt(row.Tvdb, 10, 64)
		out[index].TMDB, _ = strconv.ParseInt(row.Tmdb, 10, 64)
		out[index].AniList, _ = strconv.ParseInt(row.Anilist, 10, 64)
	}
	return out, nil
}

// MarkChecked records that the franchise lookup was made for the series.
func (f Franchises) MarkChecked(ctx context.Context, workID int64) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).MarkFranchiseChecked(ctx, librarydb.MarkFranchiseCheckedParams{WorkID: workID, CheckedAt: f.store.Now().Unix()})
	})
	if err != nil {
		return fmt.Errorf("library: franchise check of work %d: %w", workID, err)
	}
	return nil
}

// posterOf is the poster address in a work's stored pictures (a JSON list of kind and url), empty when there is none.
func posterOf(encoded string) string {
	if encoded == "" {
		return ""
	}
	var images []struct {
		Kind string `json:"kind"`
		URL  string `json:"url"`
	}
	if json.Unmarshal([]byte(encoded), &images) != nil {
		return ""
	}
	for _, image := range images {
		if image.Kind == "poster" && image.URL != "" {
			return image.URL
		}
	}
	return ""
}

// WorkName is a work with its name and year, for finding which belong together.
type WorkName struct {
	WorkID int64
	Title  string
	Year   int64
}

// WorkNames lists every work of the media type.
func (f Franchises) WorkNames(ctx context.Context, mediaType string) ([]WorkName, error) {
	rows, err := librarydb.New(f.store.Read()).ListWorkNamesOfType(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: work names: %w", err)
	}
	out := make([]WorkName, len(rows))
	for index, row := range rows {
		out[index] = WorkName{WorkID: row.WorkID, Title: row.Title, Year: row.Year}
	}
	return out, nil
}

// Membership is a work's place in a franchise, and where the franchise comes from.
type Membership struct {
	CollectionID, WorkID int64
	Source               string
}

// FranchiseMemberships lists who belongs to which franchise of the media type.
func (f Franchises) FranchiseMemberships(ctx context.Context, mediaType string) ([]Membership, error) {
	rows, err := librarydb.New(f.store.Read()).ListFranchiseMembershipsOfType(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: franchise memberships: %w", err)
	}
	out := make([]Membership, len(rows))
	for index, row := range rows {
		out[index] = Membership{CollectionID: row.CollectionID, WorkID: row.WorkID, Source: row.Source}
	}
	return out, nil
}

// RelatedKind is the relationship between two works a provider says belong together.
const RelatedKind = "related_series"

// AutoSource is the source of a franchise Solarr worked out itself.
const AutoSource = "auto"

// Relate records that a provider says two works belong together.
func (f Franchises) Relate(ctx context.Context, workID, otherID int64) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).AddRelationship(ctx, librarydb.AddRelationshipParams{FromType: "work", FromID: workID, ToType: "work", ToID: otherID, Kind: RelatedKind})
	})
	if err != nil {
		return fmt.Errorf("library: relate works %d and %d: %w", workID, otherID, err)
	}
	return nil
}

// RelatedPairs lists the works of the media type a provider said belong together.
func (f Franchises) RelatedPairs(ctx context.Context, mediaType string) ([][2]int64, error) {
	rows, err := librarydb.New(f.store.Read()).ListRelatedWorkPairs(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: related works: %w", err)
	}
	out := make([][2]int64, len(rows))
	for index, row := range rows {
		out[index] = [2]int64{row.FromID, row.ToID}
	}
	return out, nil
}

// JoinGroup puts the works in one franchise Solarr worked out: the lowest-numbered automatic franchise any of them is
// already in (the others of theirs merge into it), else a new one. It answers the franchise's id.
func (f Franchises) JoinGroup(ctx context.Context, mediaType, name string, workIDs []int64, existing []Membership) (int64, error) {
	var keep int64
	var merge []int64
	for _, membership := range existing {
		if membership.Source != AutoSource || !slices.Contains(workIDs, membership.WorkID) {
			continue
		}
		switch {
		case keep == 0 || membership.CollectionID < keep:
			if keep != 0 {
				merge = append(merge, keep)
			}
			keep = membership.CollectionID
		case membership.CollectionID != keep && !slices.Contains(merge, membership.CollectionID):
			merge = append(merge, membership.CollectionID)
		}
	}
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if keep == 0 {
			created, err := queries.CreateCollection(ctx, librarydb.CreateCollectionParams{
				MediaType: mediaType, Name: name, Kind: FranchiseKind, Source: AutoSource, AddedAt: f.store.Now().Unix(),
			})
			if err != nil {
				return err
			}
			keep = created.ID
			if err := queries.LinkCollectionExternalID(ctx, librarydb.LinkCollectionExternalIDParams{Provider: AutoSource, ExternalID: strconv.FormatInt(keep, 10), CollectionID: keep}); err != nil {
				return err
			}
		}
		for _, other := range merge {
			if err := queries.MoveCollectionMembers(ctx, librarydb.MoveCollectionMembersParams{IntoID: keep, FromID: other}); err != nil {
				return err
			}
			if err := queries.DeleteCollection(ctx, other); err != nil {
				return err
			}
		}
		for _, workID := range workIDs {
			if err := queries.AddCollectionMember(ctx, librarydb.AddCollectionMemberParams{CollectionID: keep, WorkID: workID, PartID: 0}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("library: franchise %q: %w", name, err)
	}
	return keep, nil
}
