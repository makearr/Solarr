package library

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// MissingPart is a part of an event a provider lists that the library does not hold: an episode of a series it lacks, or
// one of a series it has that is not a part there.
type MissingPart struct {
	Number                   int
	Provider, ExternalID     string
	SeriesTitle, Title, Date string
	Season, Episode          int
	// WorkID is the library's series when it holds one, 0 when the series would have to be added.
	WorkID int64
}

// seriesKey names a series by the id a provider gives it.
type seriesKey struct{ provider, externalID string }

// heldPartKey and missingPartKey name an episode in the exclusions a person's edit leaves, whichever way detection finds it.
func heldPartKey(workID int64, ref string) string { return fmt.Sprintf("w%d:%s", workID, ref) }

func missingPartKey(missing MissingPart) string {
	return fmt.Sprintf("%s:%s:%s", missing.Provider, missing.ExternalID, episodeRef(missing.Season, missing.Episode))
}

// withoutExcluded drops the episodes a person took out of the event of this name.
func withoutExcluded(ctx context.Context, queries *librarydb.Queries, key string, episodes []eventEpisode) ([]eventEpisode, error) {
	rows, err := queries.ListCrossoverExcluded(ctx, key)
	if err != nil || len(rows) == 0 {
		return episodes, err
	}
	excluded := map[string]bool{}
	for _, row := range rows {
		excluded[row] = true
	}
	return slices.DeleteFunc(slices.Clone(episodes), func(episode eventEpisode) bool {
		if episode.missing != nil {
			return excluded[missingPartKey(*episode.missing)]
		}
		return excluded[heldPartKey(episode.workID, episode.ref)]
	}), nil
}

// eventKey is what the halves of one event share: its name without a part marker, case folded.
func eventKey(name string) string {
	base, _ := eventBase(name)
	return strings.ToLower(base)
}

// EventTitle splits an episode title into its name and the part number it carries (0 when it has none).
func EventTitle(title string) (string, int) { return eventBase(title) }

// episodeRef is the reference a series' episode has as a part.
func episodeRef(season, episode int) string { return fmt.Sprintf("s%02d:e%02d", season, episode) }

type slottedEpisode struct {
	eventEpisode
	slot    int
	guessed bool
}

// slotEpisodes numbers an event's episodes: titled ones keep their number, the rest take a free one by air date (flagged
// as guesses); total is at least floor, the highest number seen and the episodes known.
func slotEpisodes(episodes []eventEpisode, floor int) ([]slottedEpisode, int) {
	ordered := slices.Clone(episodes)
	slices.SortFunc(ordered, func(a, b eventEpisode) int {
		return cmp.Or(cmp.Compare(a.number, b.number), a.at.Compare(b.at), cmp.Compare(a.partID, b.partID), strings.Compare(a.title, b.title))
	})
	taken := map[int]bool{}
	var placed []slottedEpisode
	var loose []eventEpisode
	total := max(floor, len(episodes))
	for _, episode := range ordered {
		if episode.number > 0 && !taken[episode.number] {
			taken[episode.number] = true
			total = max(total, episode.number)
			placed = append(placed, slottedEpisode{eventEpisode: episode, slot: episode.number})
			continue
		}
		loose = append(loose, episode)
	}
	slices.SortFunc(loose, func(a, b eventEpisode) int {
		return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.partID, b.partID), strings.Compare(a.title, b.title))
	})
	for _, episode := range loose {
		slot := probableSlot(episode, placed, taken, total)
		taken[slot] = true
		placed = append(placed, slottedEpisode{eventEpisode: episode, slot: slot, guessed: true})
	}
	slices.SortFunc(placed, func(a, b slottedEpisode) int { return cmp.Compare(a.slot, b.slot) })
	return placed, total
}

// probableSlot is the free number an unnumbered episode most likely has: between the numbered episodes aired before and
// after it, the first free one after the earlier, else the last free one before the later.
func probableSlot(episode eventEpisode, placed []slottedEpisode, taken map[int]bool, total int) int {
	before, after := 0, total+1
	for _, other := range placed {
		if other.at.After(episode.at) {
			after = min(after, other.slot)
		} else {
			before = max(before, other.slot)
		}
	}
	var between, free []int
	for slot := 1; slot <= total; slot++ {
		if taken[slot] {
			continue
		}
		free = append(free, slot)
		if slot > before && slot < after {
			between = append(between, slot)
		}
	}
	switch {
	case len(between) > 0 && before == 0 && after <= total:
		return between[len(between)-1]
	case len(between) > 0:
		return between[0]
	case len(free) > 0:
		return free[0]
	}
	return total + 1
}

// eventInput is what one pass knows of an event: its name, the part count a provider states and the episodes found.
type eventInput struct {
	name     string
	total    int
	episodes []eventEpisode
	// series maps a provider's id of a series to the library's work, to tell a listed part the library now holds.
	series map[seriesKey]int64
}

func parseDate(date string) time.Time {
	at, _ := time.Parse("2006-01-02", date)
	return at
}

// storeEvent writes an event into target (a new auto event when 0), folding in the events absorbed, with every part
// slotted: the members with their numbers, the guesses flagged, the parts still missing. It answers the event's id.
func storeEvent(ctx context.Context, queries *librarydb.Queries, mediaType string, now time.Time, in eventInput, target int64, absorb []int64) (int64, error) {
	var ids []int64
	if target != 0 {
		ids = append(ids, target)
	}
	ids = append(ids, absorb...)
	episodes := slices.Clone(in.episodes)
	floor := in.total
	name, monitored := in.name, int64(0)
	if len(ids) > 0 {
		held, err := heldEvents(ctx, queries, ids)
		if err != nil {
			return 0, err
		}
		episodes, floor = append(episodes, held.episodes...), max(floor, held.total)
		name, monitored = cmp.Or(held.names[ids[0]], in.name), held.monitored
		for _, id := range ids[1:] {
			if err := queries.DeleteCollection(ctx, id); err != nil {
				return 0, err
			}
		}
	}
	episodes, err := withoutExcluded(ctx, queries, eventKey(name), distinctEpisodes(episodes, in.series))
	if err != nil {
		return 0, err
	}
	slots, total := slotEpisodes(episodes, floor)
	if target == 0 {
		created, err := queries.CreateCollection(ctx, librarydb.CreateCollectionParams{
			MediaType: mediaType, Name: name, Kind: CrossoverKind, Source: AutoCrossoverSource, AddedAt: now.Unix(),
		})
		if err != nil {
			return 0, err
		}
		target = created.ID
	}
	if monitored == 1 {
		if err := queries.RenameCollection(ctx, librarydb.RenameCollectionParams{Name: name, Monitored: 1, ID: target}); err != nil {
			return 0, err
		}
	}
	for _, wipe := range []func(context.Context, int64) error{queries.DeleteCollectionMembers, queries.ClearCrossoverMemberGuesses, queries.DeleteCrossoverMissingParts} {
		if err := wipe(ctx, target); err != nil {
			return 0, err
		}
	}
	for _, slot := range slots {
		if err := writeSlot(ctx, queries, target, slot); err != nil {
			return 0, err
		}
	}
	return target, queries.UpsertCrossoverEvent(ctx, librarydb.UpsertCrossoverEventParams{CollectionID: target, NameKey: eventKey(name), TotalParts: int64(total)})
}

func writeSlot(ctx context.Context, queries *librarydb.Queries, event int64, slot slottedEpisode) error {
	if missing := slot.missing; missing != nil {
		return queries.UpsertCrossoverMissingPart(ctx, librarydb.UpsertCrossoverMissingPartParams{
			CollectionID: event, Number: int64(slot.slot), Provider: missing.Provider, ExternalID: missing.ExternalID, SeriesTitle: missing.SeriesTitle,
			Season: int64(missing.Season), Episode: int64(missing.Episode), Title: missing.Title, AirDate: missing.Date,
		})
	}
	if err := queries.AddCollectionMember(ctx, librarydb.AddCollectionMemberParams{
		CollectionID: event, WorkID: slot.workID, PartID: slot.partID, Ord: sql.NullInt64{Int64: int64(slot.slot), Valid: true},
	}); err != nil {
		return err
	}
	if slot.guessed {
		return queries.AddCrossoverMemberGuess(ctx, librarydb.AddCrossoverMemberGuessParams{CollectionID: event, PartID: slot.partID})
	}
	return nil
}

// distinctEpisodes drops a part listed twice and a missing part the library now holds.
func distinctEpisodes(episodes []eventEpisode, series map[seriesKey]int64) []eventEpisode {
	held := map[[2]any]bool{}
	for _, episode := range episodes {
		if episode.missing == nil {
			held[[2]any{episode.workID, episode.ref}] = true
		}
	}
	seen := map[any]bool{}
	var out []eventEpisode
	for _, episode := range episodes {
		var key any = episode.partID
		if missing := episode.missing; missing != nil {
			if work := series[seriesKey{missing.Provider, missing.ExternalID}]; work != 0 && held[[2]any{work, episodeRef(missing.Season, missing.Episode)}] {
				continue
			}
			key = [4]any{missing.Provider, missing.ExternalID, missing.Season, missing.Episode}
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, episode)
		}
	}
	return out
}

type heldEvent struct {
	episodes  []eventEpisode
	total     int
	names     map[int64]string
	monitored int64
}

// heldEvents reads what the events hold now as episodes to slot again: a title's part marker wins over the stored number,
// and a guessed member counts as unnumbered.
func heldEvents(ctx context.Context, queries *librarydb.Queries, ids []int64) (heldEvent, error) {
	held := heldEvent{names: map[int64]string{}}
	for _, id := range ids {
		event, err := queries.GetCollection(ctx, id)
		if err != nil {
			return held, err
		}
		held.names[id] = event.Name
		held.monitored = max(held.monitored, event.Monitored)
	}
	members, err := queries.ListPartCollectionMembers(ctx, ids)
	if err != nil {
		return held, err
	}
	guesses, err := queries.ListCrossoverMemberGuesses(ctx, ids)
	if err != nil {
		return held, err
	}
	guessed := map[[2]int64]bool{}
	for _, guess := range guesses {
		guessed[[2]int64{guess.CollectionID, guess.PartID}] = true
	}
	infos, err := queries.ListCrossoverEventInfo(ctx, ids)
	if err != nil {
		return held, err
	}
	stored := map[int64]bool{}
	for _, info := range infos {
		stored[info.CollectionID] = true
		held.total = max(held.total, int(info.TotalParts))
	}
	for _, member := range members {
		_, number := eventBase(member.PartTitle)
		if number == 0 && stored[member.CollectionID] && !guessed[[2]int64{member.CollectionID, member.PartID}] {
			number = int(member.Ord.Int64)
		}
		held.episodes = append(held.episodes, eventEpisode{
			partID: member.PartID, workID: member.WorkID, ref: member.MediaRef, number: number, title: member.PartTitle,
			date: member.Date, at: parseDate(member.Date),
		})
	}
	missing, err := queries.ListCrossoverMissingParts(ctx, ids)
	if err != nil {
		return held, err
	}
	for _, row := range missing {
		held.episodes = append(held.episodes, eventEpisode{
			number: int(row.Number), title: row.Title, date: row.AirDate, at: parseDate(row.AirDate),
			missing: &MissingPart{
				Number: int(row.Number), Provider: row.Provider, ExternalID: row.ExternalID, SeriesTitle: row.SeriesTitle,
				Title: row.Title, Date: row.AirDate, Season: int(row.Season), Episode: int(row.Episode),
			},
		})
	}
	return held, nil
}

// matchingEvents are the events a pass made (not ones a person edited) with this name whose weeks the episodes fall in.
func matchingEvents(ctx context.Context, queries *librarydb.Queries, mediaType, key string, first, last time.Time) ([]int64, error) {
	events, err := queries.ListAutoCrossovers(ctx, mediaType)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, event := range events {
		if eventKey(event.Name) != key {
			continue
		}
		from, to := parseDate(event.FirstDate), parseDate(event.LastDate)
		if event.FirstDate == "" || first.IsZero() || max(last.Unix(), to.Unix())-min(first.Unix(), from.Unix()) <= int64(eventWindow.Seconds()) {
			ids = append(ids, event.ID)
		}
	}
	return ids, nil
}

// dismissedEvent says a person deleted an event of this name in these weeks.
func dismissedEvent(ctx context.Context, queries *librarydb.Queries, key string, first time.Time) (bool, error) {
	dismissed, err := queries.ListCrossoverEventDismissed(ctx)
	if err != nil {
		return false, err
	}
	for _, row := range dismissed {
		if row.NameKey != key {
			continue
		}
		at := parseDate(row.FirstDate)
		if row.FirstDate == "" || first.IsZero() || at.Sub(first).Abs() <= eventWindow {
			return true, nil
		}
	}
	return false, nil
}

// MergeSplitEvents folds the events a pass made apart into one: the same name, aired within weeks of each other (the
// halves of "Crisis on Earth-X" found one series at a time). It answers how many events it removed.
func (c Crossovers) MergeSplitEvents(ctx context.Context, mediaType string) (int, error) {
	events, err := librarydb.New(c.store.Read()).ListAutoCrossovers(ctx, mediaType)
	if err != nil {
		return 0, fmt.Errorf("library: auto events: %w", err)
	}
	byKey := map[string][]librarydb.ListAutoCrossoversRow{}
	for _, event := range events {
		byKey[eventKey(event.Name)] = append(byKey[eventKey(event.Name)], event)
	}
	merged := 0
	for key, same := range byKey {
		if len(same) < 2 {
			continue
		}
		slices.SortFunc(same, func(a, b librarydb.ListAutoCrossoversRow) int {
			return cmp.Or(strings.Compare(a.FirstDate, b.FirstDate), cmp.Compare(a.ID, b.ID))
		})
		var clusters [][]librarydb.ListAutoCrossoversRow
		for _, event := range same {
			last := len(clusters) - 1
			if last >= 0 && sameWeeks(clusters[last], event) {
				clusters[last] = append(clusters[last], event)
			} else {
				clusters = append(clusters, []librarydb.ListAutoCrossoversRow{event})
			}
		}
		for _, cluster := range clusters {
			if len(cluster) < 2 {
				continue
			}
			ids := make([]int64, len(cluster))
			for i, event := range cluster {
				ids[i] = event.ID
			}
			slices.Sort(ids)
			err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
				_, err := storeEvent(ctx, librarydb.New(tx), mediaType, c.store.Now(), eventInput{name: cluster[0].Name}, ids[0], ids[1:])
				return err
			})
			if err != nil {
				return merged, fmt.Errorf("library: merge events %q: %w", key, err)
			}
			merged += len(ids) - 1
		}
	}
	return merged, nil
}

// sameWeeks says an event falls within one event's span of the cluster's dates.
func sameWeeks(cluster []librarydb.ListAutoCrossoversRow, event librarydb.ListAutoCrossoversRow) bool {
	first, last := parseDate(cluster[0].FirstDate), parseDate(cluster[0].LastDate)
	for _, other := range cluster {
		first, last = earlier(first, parseDate(other.FirstDate)), later(last, parseDate(other.LastDate))
	}
	from, to := parseDate(event.FirstDate), parseDate(event.LastDate)
	return event.FirstDate == "" || first.IsZero() || later(last, to).Sub(earlier(first, from)) <= eventWindow
}

func earlier(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// ProviderEventPart is one episode of an event as a provider lists it, for a series the library may or may not hold.
type ProviderEventPart struct {
	// Number is the part number the title carries, 0 when it has none.
	Number                   int
	Provider, ExternalID     string
	SeriesTitle, Title, Date string
	Season, Episode          int
}

// ProviderEvent is a crossover event found in providers' episode lists.
type ProviderEvent struct {
	Name string
	// Total is the part count the providers state, 0 when none does.
	Total int
	Parts []ProviderEventPart
}

// RecordProviderEvent keeps an event found in providers' lists: held parts become members (merged with an event of that
// name and weeks), the rest are listed as missing; false when none is held, it was deleted or two parts were ruled out.
func (c Crossovers) RecordProviderEvent(ctx context.Context, mediaType string, event ProviderEvent) (bool, error) {
	recorded := false
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		series := map[seriesKey]int64{}
		loaded := map[string]bool{}
		for _, part := range event.Parts {
			if loaded[part.Provider] {
				continue
			}
			loaded[part.Provider] = true
			rows, err := queries.ListWorkProviderIDs(ctx, librarydb.ListWorkProviderIDsParams{Provider: part.Provider, MediaType: mediaType})
			if err != nil {
				return err
			}
			for _, row := range rows {
				series[seriesKey{part.Provider, row.ExternalID}] = row.WorkID
			}
		}
		var first, last time.Time
		for _, part := range event.Parts {
			first, last = earlier(first, parseDate(part.Date)), later(last, parseDate(part.Date))
		}
		key := eventKey(event.Name)
		targets, err := matchingEvents(ctx, queries, mediaType, key, first, last)
		if err != nil {
			return err
		}
		owners, err := queries.ListCrossoverMemberPartOwners(ctx, mediaType)
		if err != nil {
			return err
		}
		ownedElsewhere := map[int64]bool{}
		for _, owner := range owners {
			if !slices.Contains(targets, owner.CollectionID) {
				ownedElsewhere[owner.PartID] = true
			}
		}
		var episodes []eventEpisode
		members := 0
		for _, part := range event.Parts {
			episode := eventEpisode{number: part.Number, title: part.Title, date: part.Date, at: parseDate(part.Date)}
			workID := series[seriesKey{part.Provider, part.ExternalID}]
			if workID != 0 {
				held, err := queries.FindWorkPart(ctx, librarydb.FindWorkPartParams{WorkID: workID, MediaRef: episodeRef(part.Season, part.Episode)})
				if err == nil {
					if ownedElsewhere[held.ID] {
						continue
					}
					episode.partID, episode.workID, episode.ref = held.ID, workID, held.MediaRef
					episodes = append(episodes, episode)
					members++
					continue
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			episode.missing = &MissingPart{
				Number: part.Number, Provider: part.Provider, ExternalID: part.ExternalID, SeriesTitle: part.SeriesTitle,
				Title: part.Title, Date: part.Date, Season: part.Season, Episode: part.Episode,
			}
			episodes = append(episodes, episode)
		}
		if members == 0 {
			return nil
		}
		if len(targets) == 0 {
			if dismissed, err := dismissedEvent(ctx, queries, key, first); err != nil || dismissed {
				return err
			}
		}
		apart, err := ruledOutPairs(ctx, queries)
		if err != nil {
			return err
		}
		if separate(episodes, apart) {
			return nil
		}
		var target int64
		if len(targets) > 0 {
			target = targets[0]
		}
		var absorb []int64
		if len(targets) > 1 {
			absorb = targets[1:]
		}
		if _, err := storeEvent(ctx, queries, mediaType, c.store.Now(), eventInput{name: event.Name, total: event.Total, episodes: episodes, series: series}, target, absorb); err != nil {
			return err
		}
		recorded = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("library: record event %q: %w", event.Name, err)
	}
	return recorded, nil
}

// separate says a person ruled out two of the episodes as one broadcast.
func separate(episodes []eventEpisode, apart map[[2]int64]bool) bool {
	for i, a := range episodes {
		for _, b := range episodes[i+1:] {
			if a.partID != 0 && b.partID != 0 && apart[[2]int64{min(a.partID, b.partID), max(a.partID, b.partID)}] {
				return true
			}
		}
	}
	return false
}

// ruledOutPairs are the episode pairs a person said are not one broadcast, by either way of saying it.
func ruledOutPairs(ctx context.Context, queries *librarydb.Queries) (map[[2]int64]bool, error) {
	apart := map[[2]int64]bool{}
	ruledOut, err := queries.ListDifferentContentPairs(ctx)
	if err != nil {
		return nil, fmt.Errorf("ruled out pairs: %w", err)
	}
	for _, pair := range ruledOut {
		apart[[2]int64{min(pair.FromID, pair.ToID), max(pair.FromID, pair.ToID)}] = true
	}
	kept, err := queries.ListCrossoverNotSameParts(ctx)
	if err != nil {
		return nil, fmt.Errorf("not the same pairs: %w", err)
	}
	for _, pair := range kept {
		apart[[2]int64{min(pair.LowPartID, pair.HighPartID), max(pair.LowPartID, pair.HighPartID)}] = true
	}
	return apart, nil
}

// MarkNotSame records that two episodes are not one broadcast: an existing link goes, and neither detection nor the
// suggestions ask about the pair again.
func (c Crossovers) MarkNotSame(ctx context.Context, partID, otherPartID int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
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
		for _, pair := range [][2]int64{{partID, otherPartID}, {otherPartID, partID}} {
			if err := queries.DeleteRelationship(ctx, librarydb.DeleteRelationshipParams{
				FromType: partSubject, FromID: pair[0], ToType: partSubject, ToID: pair[1], Kind: SameContentKind,
			}); err != nil {
				return err
			}
		}
		return queries.AddCrossoverNotSame(ctx, librarydb.AddCrossoverNotSameParams{
			LowWorkID: a.WorkID, LowRef: a.MediaRef, HighWorkID: b.WorkID, HighRef: b.MediaRef, CreatedAt: c.store.Now().Unix(),
		})
	})
	if err != nil {
		return fmt.Errorf("library: not the same %d and %d: %w", partID, otherPartID, err)
	}
	return nil
}

// FamilyCheckedAt answers when a family of series had its provider episode lists compared, false when it never had.
func (c Crossovers) FamilyCheckedAt(ctx context.Context, familyKey string) (time.Time, bool, error) {
	at, err := librarydb.New(c.store.Read()).GetCrossoverFamilyChecked(ctx, familyKey)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("library: family checked: %w", err)
	}
	return time.Unix(at, 0), true, nil
}

// ForgetFamilyChecks makes the next pass compare every family again, as when a series was just added that one of them lacked.
func (c Crossovers) ForgetFamilyChecks(ctx context.Context) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteCrossoverFamilyChecks(ctx)
	})
	if err != nil {
		return fmt.Errorf("library: forget family checks: %w", err)
	}
	return nil
}

// MarkFamilyChecked remembers a family had its episode lists compared now.
func (c Crossovers) MarkFamilyChecked(ctx context.Context, familyKey string) error {
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetCrossoverFamilyChecked(ctx, librarydb.SetCrossoverFamilyCheckedParams{FamilyKey: familyKey, CheckedAt: c.store.Now().Unix()})
	})
	if err != nil {
		return fmt.Errorf("library: mark family checked: %w", err)
	}
	return nil
}

// EventPairs lists every pair of series that share an event, once per event, named by the event.
func (c Crossovers) EventPairs(ctx context.Context, mediaType string) ([]SameContentPair, error) {
	rows, err := librarydb.New(c.store.Read()).ListCrossoverEventWorks(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: event series: %w", err)
	}
	byEvent := map[int64][]librarydb.ListCrossoverEventWorksRow{}
	var order []int64
	for _, row := range rows {
		if _, seen := byEvent[row.CollectionID]; !seen {
			order = append(order, row.CollectionID)
		}
		byEvent[row.CollectionID] = append(byEvent[row.CollectionID], row)
	}
	var pairs []SameContentPair
	for _, id := range order {
		works := byEvent[id]
		for i, a := range works {
			for _, b := range works[i+1:] {
				pairs = append(pairs, SameContentPair{WorkID: a.WorkID, OtherWorkID: b.WorkID, Title: a.Name})
				pairs = append(pairs, SameContentPair{WorkID: b.WorkID, OtherWorkID: a.WorkID, Title: a.Name})
			}
		}
	}
	return pairs, nil
}
