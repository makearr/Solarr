package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
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
	// DifferentContentKind remembers two parts a person said are not one broadcast, so detection never links them again.
	DifferentContentKind = "different_content"
	partSubject          = "part"
)

// ErrNotACrossover is an id that is no crossover.
var ErrNotACrossover = errors.New("library: no such crossover")

// Crossover is one event with its members in order.
type Crossover struct {
	ID        int64
	Name      string
	Monitored bool
	// Total is how many parts the event has: what a provider states, else the most seen.
	Total   int
	Members []CrossoverMember
	// Missing are parts of the event the library does not hold; Gaps are part numbers nothing is known for.
	Missing []MissingPart
	Gaps    []int
}

// CrossoverMember is one part of an event, with what a list shows of it.
type CrossoverMember struct {
	PartID, WorkID      int64
	WorkTitle           string
	MediaRef, PartTitle string
	Date                string
	Monitored, HasFile  bool
	// Number is the part's place in the event; Guessed says it was worked out from air dates, not from the title.
	Number  int
	Guessed bool
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

// Update renames an event and sets its members, in order, all at once. An event detection found stays under detection:
// the parts taken out are remembered so they are not put back, and its missing parts stay.
func (c Crossovers) Update(ctx context.Context, id int64, name string, partIDs []int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		event, err := crossoverTx(ctx, queries, id)
		if err != nil {
			return err
		}
		keepsDetecting := event.Source == AutoCrossoverSource && eventKey(event.Name) == eventKey(name)
		total := int64(len(partIDs))
		if keepsDetecting {
			if total, err = c.rememberRemovedParts(ctx, queries, id, name, partIDs); err != nil {
				return err
			}
		} else {
			// A renamed event is the person's: detection no longer adds to it or merges it.
			if err := queries.SetCollectionSource(ctx, librarydb.SetCollectionSourceParams{Source: "user", ID: id}); err != nil {
				return err
			}
			if err := queries.DeleteCrossoverMissingParts(ctx, id); err != nil {
				return err
			}
		}
		if err := queries.RenameCollection(ctx, librarydb.RenameCollectionParams{Name: name, Monitored: event.Monitored, ID: id}); err != nil {
			return err
		}
		if err := queries.DeleteCollectionMembers(ctx, id); err != nil {
			return err
		}
		if err := queries.ClearCrossoverMemberGuesses(ctx, id); err != nil {
			return err
		}
		if err := queries.UpsertCrossoverEvent(ctx, librarydb.UpsertCrossoverEventParams{CollectionID: id, NameKey: eventKey(name), TotalParts: total}); err != nil {
			return err
		}
		return setMembersTx(ctx, queries, id, partIDs)
	})
	if err != nil {
		return fmt.Errorf("library: update crossover %d: %w", id, err)
	}
	return nil
}

// rememberRemovedParts excludes the members an edit took out of the event, forgets the exclusion of those it put back,
// and answers the part count the event now has (its missing parts included).
func (c Crossovers) rememberRemovedParts(ctx context.Context, queries *librarydb.Queries, id int64, name string, partIDs []int64) (int64, error) {
	key := eventKey(name)
	before, err := queries.ListPartCollectionMembers(ctx, []int64{id})
	if err != nil {
		return 0, err
	}
	kept := map[int64]bool{}
	for _, partID := range partIDs {
		part, err := queries.GetWorkPart(ctx, partID)
		if err != nil {
			return 0, fmt.Errorf("part %d: %w", partID, err)
		}
		kept[partID] = true
		if err := queries.DeleteCrossoverExcluded(ctx, librarydb.DeleteCrossoverExcludedParams{NameKey: key, PartKey: heldPartKey(part.WorkID, part.MediaRef)}); err != nil {
			return 0, err
		}
	}
	for _, member := range before {
		if !kept[member.PartID] {
			if err := queries.AddCrossoverExcluded(ctx, librarydb.AddCrossoverExcludedParams{NameKey: key, PartKey: heldPartKey(member.WorkID, member.MediaRef)}); err != nil {
				return 0, err
			}
		}
	}
	missing, err := queries.CountCrossoverMissingParts(ctx, id)
	if err != nil {
		return 0, err
	}
	known, err := queries.GetCrossoverEvent(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return max(known.TotalParts, int64(len(partIDs))+missing), nil
}

// Delete removes an event; its parts and their works stay as they are.
func (c Crossovers) Delete(ctx context.Context, id int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		event, err := crossoverTx(ctx, queries, id)
		if err != nil {
			return err
		}
		if event.Source == AutoCrossoverSource {
			// A found event a person deletes is not found again: its episodes are ruled out as one broadcast, as unlinking does.
			if err := ruleOutEventTx(ctx, queries, id); err != nil {
				return err
			}
			if err := dismissEventTx(ctx, queries, event); err != nil {
				return err
			}
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
	queries := librarydb.New(c.store.Read())
	members, err := queries.ListPartCollectionMembers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("library: crossover members: %w", err)
	}
	guesses, err := queries.ListCrossoverMemberGuesses(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("library: crossover guesses: %w", err)
	}
	guessed := map[[2]int64]bool{}
	for _, guess := range guesses {
		guessed[[2]int64{guess.CollectionID, guess.PartID}] = true
	}
	for _, member := range members {
		at := index[member.CollectionID]
		events[at].Members = append(events[at].Members, CrossoverMember{
			PartID: member.PartID, WorkID: member.WorkID, WorkTitle: member.WorkTitle, MediaRef: member.MediaRef,
			PartTitle: member.PartTitle, Date: member.Date, Monitored: member.Monitored == 1, HasFile: member.HasFile,
			Number: int(member.Ord.Int64), Guessed: guessed[[2]int64{member.CollectionID, member.PartID}],
		})
	}
	infos, err := queries.ListCrossoverEventInfo(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("library: crossover totals: %w", err)
	}
	for _, info := range infos {
		events[index[info.CollectionID]].Total = int(info.TotalParts)
	}
	missing, err := queries.ListCrossoverMissingParts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("library: crossover missing parts: %w", err)
	}
	held := map[seriesKey]int64{}
	loaded := map[string]bool{}
	for _, row := range missing {
		if !loaded[row.Provider] {
			loaded[row.Provider] = true
			works, err := queries.ListWorkProviderIDs(ctx, librarydb.ListWorkProviderIDsParams{Provider: row.Provider, MediaType: rows[index[row.CollectionID]].MediaType})
			if err != nil {
				return nil, fmt.Errorf("library: crossover series: %w", err)
			}
			for _, work := range works {
				held[seriesKey{row.Provider, work.ExternalID}] = work.WorkID
			}
		}
		at := index[row.CollectionID]
		events[at].Missing = append(events[at].Missing, MissingPart{
			Number: int(row.Number), Provider: row.Provider, ExternalID: row.ExternalID, SeriesTitle: row.SeriesTitle,
			Title: row.Title, Date: row.AirDate, Season: int(row.Season), Episode: int(row.Episode), WorkID: held[seriesKey{row.Provider, row.ExternalID}],
		})
	}
	for i := range events {
		events[i].Total = max(events[i].Total, len(events[i].Members))
		known := map[int]bool{}
		for _, member := range events[i].Members {
			known[member.Number] = true
		}
		for _, part := range events[i].Missing {
			known[part.Number] = true
		}
		for number := 1; number <= events[i].Total; number++ {
			if !known[number] {
				events[i].Gaps = append(events[i].Gaps, number)
			}
		}
	}
	return events, nil
}

// dismissEventTx remembers a deleted event's name and weeks so the passes do not make it again.
func dismissEventTx(ctx context.Context, queries *librarydb.Queries, event librarydb.Collection) error {
	members, err := queries.ListPartCollectionMembers(ctx, []int64{event.ID})
	if err != nil {
		return err
	}
	first := ""
	for _, member := range members {
		if member.Date != "" && (first == "" || member.Date < first) {
			first = member.Date
		}
	}
	return queries.AddCrossoverEventDismissed(ctx, librarydb.AddCrossoverEventDismissedParams{NameKey: eventKey(event.Name), FirstDate: first})
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
		if err := forgetNotSameTx(ctx, queries, partID, otherPartID); err != nil {
			return err
		}
		_, err := coverLinkedTx(ctx, queries, []int64{partID, otherPartID}, c.store.Now())
		return err
	})
	if err != nil {
		return fmt.Errorf("library: link parts %d and %d: %w", partID, otherPartID, err)
	}
	return nil
}

// UnlinkSameContent forgets two parts were one broadcast, and that they are not, so detection leaves them be; what each
// holds stays until a scan says otherwise.
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
		return queries.AddRelationship(ctx, librarydb.AddRelationshipParams{
			FromType: partSubject, FromID: partID, ToType: partSubject, ToID: otherPartID, Kind: DifferentContentKind,
		})
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

// SameContentSuggestion is a pair of parts of two works with one title on one day: a candidate for one broadcast twice,
// with what tells whether it is.
type SameContentSuggestion struct {
	PartID, WorkID                    int64
	WorkTitle, MediaRef, Title, Date  string
	OtherPartID, OtherWorkID          int64
	OtherWorkTitle, OtherRef          string
	RuntimeMs, OtherRuntimeMs         int64
	People, OtherPeople, SharedPeople int64
	// AvailableAt are the moments the two episodes aired, 0 when not known; SharedOrganisations counts the networks the series share.
	AvailableAt, OtherAvailableAt, SharedOrganisations int64
}

// Certain says the evidence is enough to link without asking: a title that names the episode, and lengths within 5% (or
// the same minute on air), and one of: a near identical cast of three or more, the same moment on air, or a network the
// two series share with a title of more than one word.
func (s SameContentSuggestion) Certain() bool {
	if genericTitle(s.Title) {
		return false
	}
	sameAir := s.AvailableAt > 0 && s.OtherAvailableAt > 0 && max(s.AvailableAt, s.OtherAvailableAt)-min(s.AvailableAt, s.OtherAvailableAt) <= 600
	sameLength := s.RuntimeMs > 0 && s.OtherRuntimeMs > 0 && within(s.RuntimeMs, s.OtherRuntimeMs, 5)
	if !sameLength && !sameAir {
		return false
	}
	union := s.People + s.OtherPeople - s.SharedPeople
	nearIdenticalCast := s.SharedPeople >= 3 && union > 0 && s.SharedPeople*100 >= 80*union
	return nearIdenticalCast || sameAir || (s.SharedOrganisations > 0 && len(strings.Fields(s.Title)) >= 2)
}

// Plausible is false for a pair that cannot be one broadcast: a title every series has, lengths far apart, or casts that
// share no one.
func (s SameContentSuggestion) Plausible() bool {
	if genericTitle(s.Title) {
		return false
	}
	if s.RuntimeMs > 0 && s.OtherRuntimeMs > 0 && !within(s.RuntimeMs, s.OtherRuntimeMs, 20) {
		return false
	}
	return s.People == 0 || s.OtherPeople == 0 || s.SharedPeople > 0
}

// within says two lengths differ by no more than percent of the longer.
func within(a, b, percent int64) bool {
	longer := max(a, b)
	if percent <= 0 {
		return a == b
	}
	threshold := (longer/100)*percent + (longer%100)*percent/100
	return longer-min(a, b) <= threshold
}

var genericTitles = regexp.MustCompile(`^(the )?(pilot|premiere|finale|series premiere|series finale|season premiere|season finale|special|specials|trailer|recap|tba|tbd|untitled|christmas special|holiday special|(episode|part|chapter|special|part one|part two|part three)( ?[0-9ivx]+)?|[0-9]+)$`)

// genericTitle says a title any series may have, which says nothing about two episodes being one.
func genericTitle(title string) bool {
	return genericTitles.MatchString(strings.ToLower(strings.TrimSpace(title)))
}

// IsGenericEpisodeTitle says a title any series may have, which says nothing about two episodes being one.
func IsGenericEpisodeTitle(title string) bool { return genericTitle(title) }

// Suggestions answers up to limit plausible same-broadcast pairs in a library not linked or ruled out; nothing is linked.
func (c Crossovers) Suggestions(ctx context.Context, mediaType string, categoryID int64, limit int64) ([]SameContentSuggestion, error) {
	rows, err := librarydb.New(c.store.Read()).ListSameContentSuggestions(ctx, librarydb.ListSameContentSuggestionsParams{
		MediaType: mediaType, CategoryID: categoryID, PageSize: limit * 4,
	})
	if err != nil {
		return nil, fmt.Errorf("library: same-broadcast suggestions: %w", err)
	}
	var suggestions []SameContentSuggestion
	for _, row := range rows {
		suggestion := SameContentSuggestion{
			PartID: row.PartID, WorkID: row.WorkID, WorkTitle: row.WorkTitle, MediaRef: row.MediaRef, Title: row.Title, Date: row.Date,
			OtherPartID: row.OtherPartID, OtherWorkID: row.OtherWorkID, OtherWorkTitle: row.OtherWorkTitle, OtherRef: row.OtherRef,
			RuntimeMs: row.RuntimeMs, OtherRuntimeMs: row.OtherRuntimeMs,
			People: row.People, OtherPeople: row.OtherPeople, SharedPeople: row.SharedPeople,
			AvailableAt: row.AvailableAt, OtherAvailableAt: row.OtherAvailableAt, SharedOrganisations: row.SharedOrganisations,
		}
		if suggestion.Plausible() {
			suggestions = append(suggestions, suggestion)
		}
		if int64(len(suggestions)) >= limit {
			break
		}
	}
	return suggestions, nil
}

// LinkCertain links every pair the evidence settles (SameContentSuggestion.Certain) and answers how many, so crossover
// episodes are found without being asked; what a person rules out stays out.
func (c Crossovers) LinkCertain(ctx context.Context, mediaType string, categoryID int64) (int, error) {
	linked := 0
	for {
		batch, err := c.Suggestions(ctx, mediaType, categoryID, 200)
		if err != nil {
			return linked, err
		}
		progressed := false
		for _, suggestion := range batch {
			if !suggestion.Certain() {
				continue
			}
			if err := c.LinkSameContent(ctx, suggestion.PartID, suggestion.OtherPartID); err != nil {
				return linked, err
			}
			linked++
			progressed = true
		}
		if !progressed {
			return linked, nil
		}
	}
}

// coverLinkedTx lets each of the parts' files cover every part it is the same broadcast as, re-counts their works, and
// answers how many links it followed.
func coverLinkedTx(ctx context.Context, queries *librarydb.Queries, partIDs []int64, now time.Time) (int, error) {
	links, err := queries.ListSameContentParts(ctx, librarydb.ListSameContentPartsParams{PartIds: partIDs, PartIdsAgain: partIDs})
	if err != nil || len(links) == 0 {
		return 0, err
	}
	works := map[int64]bool{}
	for _, link := range links {
		if err := queries.CoverPartLikeAnother(ctx, librarydb.CoverPartLikeAnotherParams{TargetPartID: link.OtherPartID, SourcePartID: link.PartID}); err != nil {
			return 0, err
		}
		works[link.OtherWorkID] = true
	}
	for workID := range works {
		if err := recomputeWorkItemsTx(ctx, queries, workID, now); err != nil {
			return 0, err
		}
	}
	return len(links), nil
}

// SameContentPair is one same-broadcast link seen from one side: a part, and the part of another work it also is.
type SameContentPair struct {
	PartID, WorkID           int64
	MediaRef, Title, Date    string
	OtherPartID, OtherWorkID int64
	OtherRef                 string
	// CategoryID is the library of the part's work.
	CategoryID int64
}

// Pairs answers every same-broadcast link of a media type, once from each side.
func (c Crossovers) Pairs(ctx context.Context, mediaType string) ([]SameContentPair, error) {
	rows, err := librarydb.New(c.store.Read()).ListSameContentPairs(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: same-broadcast pairs: %w", err)
	}
	pairs := make([]SameContentPair, len(rows))
	for i, row := range rows {
		pairs[i] = SameContentPair(row)
	}
	return pairs, nil
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

func ruleOutEventTx(ctx context.Context, queries *librarydb.Queries, id int64) error {
	members, err := queries.ListCollectionMembers(ctx, id)
	if err != nil {
		return err
	}
	for i, a := range members {
		for _, b := range members[i+1:] {
			if a.PartID == 0 || b.PartID == 0 {
				continue
			}
			if err := queries.AddRelationship(ctx, librarydb.AddRelationshipParams{
				FromType: partSubject, FromID: a.PartID, ToType: partSubject, ToID: b.PartID, Kind: DifferentContentKind,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// forgetNotSameTx drops a person's earlier "not the same" for two parts they now link.
func forgetNotSameTx(ctx context.Context, queries *librarydb.Queries, partID, otherPartID int64) error {
	a, err := queries.GetWorkPart(ctx, partID)
	if err != nil {
		return err
	}
	b, err := queries.GetWorkPart(ctx, otherPartID)
	if err != nil {
		return err
	}
	if a.WorkID > b.WorkID || a.WorkID == b.WorkID && a.MediaRef > b.MediaRef {
		a, b = b, a
	}
	return queries.DeleteCrossoverNotSame(ctx, librarydb.DeleteCrossoverNotSameParams{LowWorkID: a.WorkID, LowRef: a.MediaRef, HighWorkID: b.WorkID, HighRef: b.MediaRef})
}
