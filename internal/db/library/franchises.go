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
// person monitored stays monitored. It answers the collection's id, 0 when a person deleted it or took the work out.
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
	*id = 0
	refused, err := queries.CountCollectionRefusals(ctx, librarydb.CountCollectionRefusalsParams{Provider: provider, ExternalID: externalID, WorkID: workID})
	if err != nil || refused > 0 {
		return err
	}
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
	// ExternalIDs are every id the franchise is known by, as "provider:id"; a merged franchise has several.
	ExternalIDs []string
	Monitored   bool
	PosterURL   string
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

// ErrNotCustomCollection is a collection that is not a hand-made one, or none: only those are deleted outright.
var ErrNotCustomCollection = errors.New("library: not a collection made by hand")

// Rename renames a collection of any kind; a provider's or Solarr's keeps the name from then on.
func (f Franchises) Rename(ctx context.Context, id int64, name string) error {
	return f.customWrite(ctx, id, func(queries *librarydb.Queries) (int64, error) {
		return queries.RenameCollectionByHand(ctx, librarydb.RenameCollectionByHandParams{Name: name, ID: id})
	})
}

// Delete removes a hand-made collection, never the works in it.
func (f Franchises) Delete(ctx context.Context, id int64) error {
	return f.customWrite(ctx, id, func(queries *librarydb.Queries) (int64, error) { return queries.DeleteCustomCollection(ctx, id) })
}

// AddMember puts a work in a collection of any kind; in a provider's or Solarr's it stays until a person takes it out.
func (f Franchises) AddMember(ctx context.Context, id, workID int64) error {
	return f.customWrite(ctx, id, func(queries *librarydb.Queries) (int64, error) {
		if _, err := queries.GetCollection(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		} else if err != nil {
			return 0, err
		}
		return 1, queries.AddCollectionMemberByHand(ctx, librarydb.AddCollectionMemberByHandParams{CollectionID: id, WorkID: workID})
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
	if all, err := queries.ListExternalIDsOfCollection(ctx, id); err == nil {
		for _, one := range all {
			row.ExternalIDs = append(row.ExternalIDs, one.Provider+":"+one.ExternalID)
		}
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

// SeriesIDs is a series with the ids Wikidata knows it by, 0 for one it lacks.
type SeriesIDs struct {
	WorkID     int64
	TVDB, TMDB int64
}

// SeriesIDs lists every series with its TheTVDB and TMDB ids.
func (f Franchises) SeriesIDs(ctx context.Context) ([]SeriesIDs, error) {
	rows, err := librarydb.New(f.store.Read()).ListSeriesProviderIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: series ids: %w", err)
	}
	out := make([]SeriesIDs, len(rows))
	for index, row := range rows {
		out[index] = SeriesIDs{WorkID: row.WorkID}
		out[index].TVDB, _ = strconv.ParseInt(row.Tvdb, 10, 64)
		out[index].TMDB, _ = strconv.ParseInt(row.Tmdb, 10, 64)
	}
	return out, nil
}

// WorkProviderIDs lists the works of a media type with the id a provider knows them by, work -> id.
func (f Franchises) WorkProviderIDs(ctx context.Context, mediaType, provider string) (map[int64]string, error) {
	rows, err := librarydb.New(f.store.Read()).ListWorkProviderIDs(ctx, librarydb.ListWorkProviderIDsParams{MediaType: mediaType, Provider: provider})
	if err != nil {
		return nil, fmt.Errorf("library: %s ids of %s works: %w", provider, mediaType, err)
	}
	out := make(map[int64]string, len(rows))
	for _, row := range rows {
		out[row.WorkID] = row.ExternalID
	}
	return out, nil
}

// MembersOf lists the works of a collection.
func (f Franchises) MembersOf(ctx context.Context, id int64) ([]int64, error) {
	rows, err := librarydb.New(f.store.Read()).ListCollectionMembers(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("library: members of collection %d: %w", id, err)
	}
	var out []int64
	for _, row := range rows {
		if row.PartID == 0 && !slices.Contains(out, row.WorkID) {
			out = append(out, row.WorkID)
		}
	}
	return out, nil
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

// FranchiseSummary is a franchise as the reconciliation sees it: where it came from and the ids it is known by.
type FranchiseSummary struct {
	ID          int64
	Name        string
	Source      string
	Monitored   bool
	ExternalIDs []string
}

// Summaries lists every franchise of the media type with the ids it is known by, as "provider:id".
func (f Franchises) Summaries(ctx context.Context, mediaType string) ([]FranchiseSummary, error) {
	queries := librarydb.New(f.store.Read())
	rows, err := queries.ListFranchisesOfType(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: franchises: %w", err)
	}
	out := make([]FranchiseSummary, len(rows))
	at := make(map[int64]int, len(rows))
	for index, row := range rows {
		out[index] = FranchiseSummary{ID: row.ID, Name: row.Name, Source: row.Source, Monitored: row.Monitored == 1}
		at[row.ID] = index
	}
	ids, err := queries.ListFranchiseExternalIDsOfType(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: franchise ids: %w", err)
	}
	for _, row := range ids {
		if index, ok := at[row.CollectionID]; ok {
			out[index].ExternalIDs = append(out[index].ExternalIDs, row.Provider+":"+row.ExternalID)
		}
	}
	return out, nil
}

// CreateAuto makes an empty franchise Solarr worked out itself and answers its id.
func (f Franchises) CreateAuto(ctx context.Context, mediaType, name string) (int64, error) {
	var id int64
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		created, err := queries.CreateCollection(ctx, librarydb.CreateCollectionParams{
			MediaType: mediaType, Name: name, Kind: FranchiseKind, Source: AutoSource, AddedAt: f.store.Now().Unix(),
		})
		if err != nil {
			return err
		}
		id = created.ID
		return queries.LinkCollectionExternalID(ctx, librarydb.LinkCollectionExternalIDParams{Provider: AutoSource, ExternalID: strconv.FormatInt(id, 10), CollectionID: id})
	})
	if err != nil {
		return 0, fmt.Errorf("library: franchise %q: %w", name, err)
	}
	return id, nil
}

// MergeFranchises moves the members and ids of the others into the franchise kept and deletes them.
func (f Franchises) MergeFranchises(ctx context.Context, keep int64, others []int64) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, other := range others {
			if other == keep {
				continue
			}
			if err := queries.MoveCollectionMembers(ctx, librarydb.MoveCollectionMembersParams{IntoID: keep, FromID: other}); err != nil {
				return err
			}
			if err := queries.MoveCollectionExternalIDs(ctx, librarydb.MoveCollectionExternalIDsParams{IntoID: keep, FromID: other}); err != nil {
				return err
			}
			if err := queries.DeleteCollection(ctx, other); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: merge franchises into %d: %w", keep, err)
	}
	return nil
}

// MergeSameNamed merges the collections of one media type and source that share a name, as a provider that gives one
// series several ids leaves them; it answers how many collections it removed.
func (f Franchises) MergeSameNamed(ctx context.Context, mediaType, source string) (int, error) {
	rows, err := f.store.Read().QueryContext(ctx,
		`SELECT id, LOWER(name) FROM collection WHERE media_type = ? AND source = ? AND kind = ? ORDER BY id`, mediaType, source, FranchiseKind)
	if err != nil {
		return 0, fmt.Errorf("library: list collections to merge: %w", err)
	}
	defer func() { _ = rows.Close() }()
	groups := map[string][]int64{}
	var order []string
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return 0, fmt.Errorf("library: scan collection: %w", err)
		}
		if _, seen := groups[name]; !seen {
			order = append(order, name)
		}
		groups[name] = append(groups[name], id)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("library: list collections to merge: %w", err)
	}
	_ = rows.Close()
	removed := 0
	for _, name := range order {
		ids := groups[name]
		if len(ids) < 2 {
			continue
		}
		if err := f.MergeFranchises(ctx, ids[0], ids[1:]); err != nil {
			return removed, err
		}
		removed += len(ids) - 1
	}
	return removed, nil
}

// UnrelatedKind is the relationship between two works a person said do not belong together.
const UnrelatedKind = "unrelated_series"

// UnrelatedPairs lists the works of the media type a person said do not belong together.
func (f Franchises) UnrelatedPairs(ctx context.Context, mediaType string) (map[[2]int64]bool, error) {
	rows, err := librarydb.New(f.store.Read()).ListUnrelatedWorkPairs(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: unrelated works: %w", err)
	}
	out := make(map[[2]int64]bool, len(rows)*2)
	for _, row := range rows {
		out[[2]int64{row.FromID, row.ToID}], out[[2]int64{row.ToID, row.FromID}] = true, true
	}
	return out, nil
}

// Dismiss takes a work out of a franchise a provider or Solarr made, and remembers it does not belong with the rest of
// them, so no pass puts it back.
func (f Franchises) Dismiss(ctx context.Context, franchiseID, workID int64) error {
	err := f.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		members, err := queries.ListCollectionMembers(ctx, franchiseID)
		if err != nil {
			return err
		}
		for _, member := range members {
			if member.WorkID == workID || member.PartID != 0 {
				continue
			}
			if err := queries.AddRelationship(ctx, librarydb.AddRelationshipParams{FromType: "work", FromID: workID, ToType: "work", ToID: member.WorkID, Kind: UnrelatedKind}); err != nil {
				return err
			}
		}
		if err := refuseCollection(ctx, queries, franchiseID, workID); err != nil {
			return err
		}
		return queries.RemoveCollectionMember(ctx, librarydb.RemoveCollectionMemberParams{CollectionID: franchiseID, WorkID: workID})
	})
	if err != nil {
		return fmt.Errorf("library: dismiss work %d from franchise %d: %w", workID, franchiseID, err)
	}
	return nil
}

// DeleteFranchise deletes a franchise a provider or Solarr made and remembers its works do not belong together.
func (f Franchises) DeleteFranchise(ctx context.Context, franchiseID int64) error {
	err := f.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		members, err := queries.ListCollectionMembers(ctx, franchiseID)
		if err != nil {
			return err
		}
		for i, one := range members {
			for _, other := range members[i+1:] {
				if one.PartID != 0 || other.PartID != 0 {
					continue
				}
				if err := queries.AddRelationship(ctx, librarydb.AddRelationshipParams{FromType: "work", FromID: one.WorkID, ToType: "work", ToID: other.WorkID, Kind: UnrelatedKind}); err != nil {
					return err
				}
			}
		}
		if err := refuseCollection(ctx, queries, franchiseID, 0); err != nil {
			return err
		}
		return queries.DeleteCollection(ctx, franchiseID)
	})
	if err != nil {
		return fmt.Errorf("library: delete franchise %d: %w", franchiseID, err)
	}
	return nil
}

// refuseCollection remembers, under each provider id of the collection, that a person deleted it (workID 0) or took the
// work out of it, so Join never makes it or puts the work back.
func refuseCollection(ctx context.Context, queries *librarydb.Queries, collectionID, workID int64) error {
	externals, err := queries.ListExternalIDsOfCollection(ctx, collectionID)
	if err != nil {
		return err
	}
	for _, external := range externals {
		if err := queries.AddCollectionRefusal(ctx, librarydb.AddCollectionRefusalParams{
			Provider: external.Provider, ExternalID: external.ExternalID, WorkID: workID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// UncheckedAnime is an anime series whose AniList relations have not been read.
type UncheckedAnime struct{ WorkID, AniList int64 }

// AnimeWithoutRelationCheck lists up to limit entries (a series may have several) not yet crawled.
func (f Franchises) AnimeWithoutRelationCheck(ctx context.Context, limit int64) ([]UncheckedAnime, error) {
	rows, err := librarydb.New(f.store.Read()).ListAnimeWithoutRelationCheck(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("library: anime without a relation check: %w", err)
	}
	out := make([]UncheckedAnime, 0, len(rows))
	for _, row := range rows {
		if id, err := strconv.ParseInt(row.Anilist, 10, 64); err == nil {
			out = append(out, UncheckedAnime{WorkID: row.WorkID, AniList: id})
		}
	}
	return out, nil
}

// MarkAnimeChecked records that the anime's relations were read.
func (f Franchises) MarkAnimeChecked(ctx context.Context, workID int64) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).MarkAnimeChecked(ctx, librarydb.MarkAnimeCheckedParams{WorkID: workID, CheckedAt: f.store.Now().Unix()})
	})
	if err != nil {
		return fmt.Errorf("library: anime check of work %d: %w", workID, err)
	}
	return nil
}

// SetTotal keeps how many works the provider says a franchise has.
func (f Franchises) SetTotal(ctx context.Context, id, total int64) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetCollectionComplete(ctx, librarydb.SetCollectionCompleteParams{CompleteAt: sql.NullInt64{Int64: total, Valid: total > 0}, ID: id})
	})
	if err != nil {
		return fmt.Errorf("library: franchise %d total: %w", id, err)
	}
	return nil
}

// Traits are what two works share when they belong together: genres, the networks or studios they are on, and the people
// who made them.
type Traits struct {
	Genres, Creators []string
	Organisations    []int64
}

// WorkTraits lists the traits of every work of the media type, by work.
func (f Franchises) WorkTraits(ctx context.Context, mediaType string) (map[int64]*Traits, error) {
	queries := librarydb.New(f.store.Read())
	traits := map[int64]*Traits{}
	of := func(id int64) *Traits {
		if traits[id] == nil {
			traits[id] = &Traits{}
		}
		return traits[id]
	}
	genres, err := queries.ListGenresOfType(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: genres: %w", err)
	}
	for _, row := range genres {
		of(row.WorkID).Genres = append(of(row.WorkID).Genres, row.Genre)
	}
	organisations, err := queries.ListOrganisationLinksOfType(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: organisations: %w", err)
	}
	for _, row := range organisations {
		of(row.WorkID).Organisations = append(of(row.WorkID).Organisations, row.OrganisationID)
	}
	creators, err := queries.ListCreatorLinksOfType(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: creators: %w", err)
	}
	for _, row := range creators {
		of(row.WorkID).Creators = append(of(row.WorkID).Creators, strconv.FormatInt(row.PersonID, 10))
	}
	return traits, nil
}

// CharacterCredit is a character someone plays in a work; Regular when they play it in the series itself, not only in
// an episode, Episodes the episodes it is credited in, and Billing its place among the series' cast, -1 when it has none.
type CharacterCredit struct {
	WorkID, PersonID  int64
	Person, Character string
	Regular           bool
	Episodes, Billing int64
}

// CharacterCredits lists who plays whom in every work of the media type.
func (f Franchises) CharacterCredits(ctx context.Context, mediaType string) ([]CharacterCredit, error) {
	rows, err := librarydb.New(f.store.Read()).ListCharacterCreditsOfType(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: characters: %w", err)
	}
	out := make([]CharacterCredit, len(rows))
	for index, row := range rows {
		out[index] = CharacterCredit{WorkID: row.WorkID, PersonID: row.PersonID, Person: row.PersonName, Character: row.Character, Regular: row.Regular == 1,
			Episodes: row.Appearances - row.Regular, Billing: row.Billing}
	}
	return out, nil
}

// RemoveFromFranchise takes works out of a franchise Solarr made, without remembering anything: the evidence for them is gone.
func (f Franchises) RemoveFromFranchise(ctx context.Context, id int64, workIDs []int64) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, workID := range workIDs {
			if err := queries.RemoveAutoCollectionMember(ctx, librarydb.RemoveAutoCollectionMemberParams{CollectionID: id, WorkID: workID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: remove from franchise %d: %w", id, err)
	}
	return nil
}

// DropFranchise deletes a franchise Solarr made whose family is gone; unlike DeleteFranchise it remembers nothing.
func (f Franchises) DropFranchise(ctx context.Context, id int64) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteCollection(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("library: drop franchise %d: %w", id, err)
	}
	return nil
}

// SetFranchiseName gives a franchise the name its works share.
func (f Franchises) SetFranchiseName(ctx context.Context, id int64, name string) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetCollectionName(ctx, librarydb.SetCollectionNameParams{Name: name, ID: id})
	})
	if err != nil {
		return fmt.Errorf("library: franchise %d name: %w", id, err)
	}
	return nil
}

// AddToFranchise puts works in a franchise that is already there.
func (f Franchises) AddToFranchise(ctx context.Context, id int64, workIDs []int64) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, workID := range workIDs {
			if err := queries.AddCollectionMember(ctx, librarydb.AddCollectionMemberParams{CollectionID: id, WorkID: workID, PartID: 0}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: add to franchise %d: %w", id, err)
	}
	return nil
}
