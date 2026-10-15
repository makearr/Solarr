package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Crossovers are events across works (Stage 8 T23): a collection of kind crossover whose members are parts, in watch
// order, and parts that are one broadcast listed under two works (relationship same_content), held once for both.
type Crossovers struct{ store *store.Store }

// NewCrossovers builds it over an open library store.
func NewCrossovers(opened *store.Store) Crossovers { return Crossovers{store: opened} }

// The collection kind and the relationship kind a crossover is stored as.
const (
	CrossoverKind   = "crossover"
	SameContentKind = "same_content"
	partSubject     = "part"
)

// ErrNotACrossover is an id that is no crossover.
var ErrNotACrossover = errors.New("library: no such crossover")

// Crossover is one event with its members in order.
type Crossover struct {
	ID        int64
	Name      string
	Monitored bool
	Members   []CrossoverMember
}

// CrossoverMember is one part of an event, with what a list shows of it.
type CrossoverMember struct {
	PartID, WorkID      int64
	WorkTitle           string
	MediaRef, PartTitle string
	Date                string
	Monitored, HasFile  bool
}

// SameContent is one part listed as the same broadcast as a part of another work.
type SameContent struct {
	PartID, OtherPartID, OtherWorkID int64
	OtherRef, OtherWorkTitle         string
}

// Create records an event of the parts, in the order given.
func (c Crossovers) Create(ctx context.Context, mediaType, name string, partIDs []int64) (int64, error) {
	var id int64
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		created, err := queries.CreateCollection(ctx, librarydb.CreateCollectionParams{
			MediaType: mediaType, Name: name, Kind: CrossoverKind, Source: "user", AddedAt: c.store.Now().Unix(),
		})
		if err != nil {
			return err
		}
		id = created.ID
		return setMembersTx(ctx, queries, id, partIDs)
	})
	if err != nil {
		return 0, fmt.Errorf("library: create crossover: %w", err)
	}
	return id, nil
}

// Update renames an event and sets its members, in order, all at once.
func (c Crossovers) Update(ctx context.Context, id int64, name string, partIDs []int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		event, err := crossoverTx(ctx, queries, id)
		if err != nil {
			return err
		}
		if err := queries.RenameCollection(ctx, librarydb.RenameCollectionParams{Name: name, Monitored: event.Monitored, ID: id}); err != nil {
			return err
		}
		if err := queries.DeleteCollectionMembers(ctx, id); err != nil {
			return err
		}
		return setMembersTx(ctx, queries, id, partIDs)
	})
	if err != nil {
		return fmt.Errorf("library: update crossover %d: %w", id, err)
	}
	return nil
}

// Delete removes an event; its parts and their works stay as they are.
func (c Crossovers) Delete(ctx context.Context, id int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if _, err := crossoverTx(ctx, queries, id); err != nil {
			return err
		}
		return queries.DeleteCollection(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("library: delete crossover %d: %w", id, err)
	}
	return nil
}

func crossoverTx(ctx context.Context, queries *librarydb.Queries, id int64) (librarydb.Collection, error) {
	event, err := queries.GetCollection(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && event.Kind != CrossoverKind {
		return librarydb.Collection{}, ErrNotACrossover
	}
	return event, err
}

func setMembersTx(ctx context.Context, queries *librarydb.Queries, id int64, partIDs []int64) error {
	for ord, partID := range partIDs {
		part, err := queries.GetWorkPart(ctx, partID)
		if err != nil {
			return fmt.Errorf("part %d: %w", partID, err)
		}
		if err := queries.AddCollectionMember(ctx, librarydb.AddCollectionMemberParams{
			CollectionID: id, WorkID: part.WorkID, PartID: partID, Ord: sql.NullInt64{Int64: int64(ord + 1), Valid: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

// Get answers one event with its members.
func (c Crossovers) Get(ctx context.Context, id int64) (Crossover, error) {
	queries := librarydb.New(c.store.Read())
	event, err := crossoverTx(ctx, queries, id)
	if err != nil {
		return Crossover{}, err
	}
	events, err := c.withMembers(ctx, []librarydb.Collection{event})
	if err != nil {
		return Crossover{}, err
	}
	return events[0], nil
}

// List answers every event of a media type, with members.
func (c Crossovers) List(ctx context.Context, mediaType string) ([]Crossover, error) {
	rows, err := librarydb.New(c.store.Read()).ListCollectionsOfKind(ctx, librarydb.ListCollectionsOfKindParams{Kind: CrossoverKind, MediaType: mediaType})
	if err != nil {
		return nil, fmt.Errorf("library: crossovers: %w", err)
	}
	return c.withMembers(ctx, rows)
}

// ForWork answers the events a part of the work is in, with all their members.
func (c Crossovers) ForWork(ctx context.Context, workID int64) ([]Crossover, error) {
	rows, err := librarydb.New(c.store.Read()).ListCollectionsOfWorkParts(ctx, librarydb.ListCollectionsOfWorkPartsParams{WorkID: workID, Kind: CrossoverKind})
	if err != nil {
		return nil, fmt.Errorf("library: crossovers of work %d: %w", workID, err)
	}
	return c.withMembers(ctx, rows)
}

func (c Crossovers) withMembers(ctx context.Context, rows []librarydb.Collection) ([]Crossover, error) {
	events := make([]Crossover, len(rows))
	index := map[int64]int{}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		events[i] = Crossover{ID: row.ID, Name: row.Name, Monitored: row.Monitored == 1}
		index[row.ID], ids[i] = i, row.ID
	}
	if len(ids) == 0 {
		return events, nil
	}
	members, err := librarydb.New(c.store.Read()).ListPartCollectionMembers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("library: crossover members: %w", err)
	}
	for _, member := range members {
		at := index[member.CollectionID]
		events[at].Members = append(events[at].Members, CrossoverMember{
			PartID: member.PartID, WorkID: member.WorkID, WorkTitle: member.WorkTitle, MediaRef: member.MediaRef,
			PartTitle: member.PartTitle, Date: member.Date, Monitored: member.Monitored == 1, HasFile: member.HasFile,
		})
	}
	return events, nil
}

// SetMonitored monitors or unmonitors every part of the event, each season following, and remembers the choice.
func (c Crossovers) SetMonitored(ctx context.Context, desired Desired, id int64, monitored bool) error {
	event, err := c.Get(ctx, id)
	if err != nil {
		return err
	}
	byWork := map[int64]map[int64]bool{}
	for _, member := range event.Members {
		if byWork[member.WorkID] == nil {
			byWork[member.WorkID] = map[int64]bool{}
		}
		byWork[member.WorkID][member.PartID] = monitored
	}
	for workID, parts := range byWork {
		if err := desired.ApplyPartMonitoring(ctx, workID, parts); err != nil {
			return err
		}
	}
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RenameCollection(ctx, librarydb.RenameCollectionParams{Name: event.Name, Monitored: boolInt(monitored), ID: id})
	})
	if err != nil {
		return fmt.Errorf("library: monitor crossover %d: %w", id, err)
	}
	return nil
}

// LinkSameContent records two parts as one broadcast and lets each one's file cover the other, both works re-counted.
func (c Crossovers) LinkSameContent(ctx context.Context, partID, otherPartID int64) error {
	if partID == otherPartID {
		return fmt.Errorf("library: a part is not the same broadcast as itself")
	}
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.AddRelationship(ctx, librarydb.AddRelationshipParams{
			FromType: partSubject, FromID: partID, ToType: partSubject, ToID: otherPartID, Kind: SameContentKind,
		}); err != nil {
			return err
		}
		return coverLinkedTx(ctx, queries, []int64{partID, otherPartID}, c.store.Now())
	})
	if err != nil {
		return fmt.Errorf("library: link parts %d and %d: %w", partID, otherPartID, err)
	}
	return nil
}

// UnlinkSameContent forgets two parts were one broadcast; what each holds stays until a scan says otherwise.
func (c Crossovers) UnlinkSameContent(ctx context.Context, partID, otherPartID int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, pair := range [][2]int64{{partID, otherPartID}, {otherPartID, partID}} {
			if err := queries.DeleteRelationship(ctx, librarydb.DeleteRelationshipParams{
				FromType: partSubject, FromID: pair[0], ToType: partSubject, ToID: pair[1], Kind: SameContentKind,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: unlink parts %d and %d: %w", partID, otherPartID, err)
	}
	return nil
}

// SameContentOf answers the same-broadcast links of the parts.
func (c Crossovers) SameContentOf(ctx context.Context, partIDs []int64) ([]SameContent, error) {
	if len(partIDs) == 0 {
		return nil, nil
	}
	rows, err := librarydb.New(c.store.Read()).ListSameContentParts(ctx, librarydb.ListSameContentPartsParams{PartIds: partIDs, PartIdsAgain: partIDs})
	if err != nil {
		return nil, fmt.Errorf("library: same-broadcast links: %w", err)
	}
	links := make([]SameContent, len(rows))
	for i, row := range rows {
		links[i] = SameContent{PartID: row.PartID, OtherPartID: row.OtherPartID, OtherWorkID: row.OtherWorkID, OtherRef: row.OtherRef, OtherWorkTitle: row.OtherWorkTitle}
	}
	return links, nil
}

// SameContentSuggestion is a pair of parts of two works with one title on one day: likely one broadcast twice.
type SameContentSuggestion struct {
	PartID, WorkID                   int64
	WorkTitle, MediaRef, Title, Date string
	OtherPartID, OtherWorkID         int64
	OtherWorkTitle, OtherRef         string
}

// Suggestions answers up to limit likely same-broadcast pairs in a library not linked yet; nothing is linked.
func (c Crossovers) Suggestions(ctx context.Context, mediaType string, categoryID int64, limit int64) ([]SameContentSuggestion, error) {
	rows, err := librarydb.New(c.store.Read()).ListSameContentSuggestions(ctx, librarydb.ListSameContentSuggestionsParams{
		MediaType: mediaType, CategoryID: categoryID, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("library: same-broadcast suggestions: %w", err)
	}
	suggestions := make([]SameContentSuggestion, len(rows))
	for i, row := range rows {
		suggestions[i] = SameContentSuggestion{
			PartID: row.PartID, WorkID: row.WorkID, WorkTitle: row.WorkTitle, MediaRef: row.MediaRef, Title: row.Title, Date: row.Date,
			OtherPartID: row.OtherPartID, OtherWorkID: row.OtherWorkID, OtherWorkTitle: row.OtherWorkTitle, OtherRef: row.OtherRef,
		}
	}
	return suggestions, nil
}

// coverLinkedTx lets each of the parts' files cover every part it is the same broadcast as, and re-counts their works.
func coverLinkedTx(ctx context.Context, queries *librarydb.Queries, partIDs []int64, now time.Time) error {
	links, err := queries.ListSameContentParts(ctx, librarydb.ListSameContentPartsParams{PartIds: partIDs, PartIdsAgain: partIDs})
	if err != nil || len(links) == 0 {
		return err
	}
	works := map[int64]bool{}
	for _, link := range links {
		if err := queries.CoverPartLikeAnother(ctx, librarydb.CoverPartLikeAnotherParams{TargetPartID: link.OtherPartID, SourcePartID: link.PartID}); err != nil {
			return err
		}
		works[link.OtherWorkID] = true
	}
	for workID := range works {
		if err := recomputeWorkItemsTx(ctx, queries, workID, now); err != nil {
			return err
		}
	}
	return nil
}

// SameContentOfWork answers the same-broadcast links of every part of the work.
func (c Crossovers) SameContentOfWork(ctx context.Context, workID int64) ([]SameContent, error) {
	parts, err := librarydb.New(c.store.Read()).ListWorkParts(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: parts of work %d: %w", workID, err)
	}
	ids := make([]int64, len(parts))
	for i, part := range parts {
		ids[i] = part.ID
	}
	return c.SameContentOf(ctx, ids)
}
