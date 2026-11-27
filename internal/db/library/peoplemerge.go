package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ArtistMerge is one artist folded into another: the loser's credits, ids, tags and subscriptions move
// to the winner and the loser is deleted (M1g 1.2). Files stay where they are.
type ArtistMerge struct {
	LoserID, WinnerID     int64
	LoserName, WinnerName string
}

type mergeCandidate struct {
	id         int64
	name       string
	identified bool
	works      int64
	keys       map[string]bool
}

// PlanArtistMerges is what MergeArtists would do for a library, changing nothing. A provider-identified
// artist takes in every local one named like it or like one of its aliases; local artists named alike
// merge into the one with the most albums. Two identified artists never merge, nor does an ambiguous match.
func (p People) PlanArtistMerges(ctx context.Context, categoryID int64) ([]ArtistMerge, error) {
	queries := librarydb.New(p.store.Read())
	rows, err := queries.ListMergeCandidatesForCategory(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: list merge candidates: %w", err)
	}
	candidates := make([]*mergeCandidate, len(rows))
	byID := make(map[int64]*mergeCandidate, len(rows))
	var identifiedIDs []int64
	for index, row := range rows {
		candidate := &mergeCandidate{
			id: row.ID, name: row.Name, identified: row.Identified == 1, works: row.Works,
			keys: map[string]bool{artistMergeKey(row.Name): true},
		}
		candidates[index] = candidate
		byID[row.ID] = candidate
		if candidate.identified {
			identifiedIDs = append(identifiedIDs, row.ID)
		}
	}
	for chunk := range slices.Chunk(identifiedIDs, 500) {
		aliases, err := queries.ListAliasesForPeople(ctx, chunk)
		if err != nil {
			return nil, fmt.Errorf("library: list aliases for merge: %w", err)
		}
		for _, alias := range aliases {
			byID[alias.PersonID].keys[artistMergeKey(alias.Alias)] = true
		}
	}
	sharedLoser := make(map[int64]bool, len(rows))
	for _, row := range rows {
		sharedLoser[row.ID] = row.Shared == 1
	}
	return planArtistMerges(candidates, sharedLoser), nil
}

func planArtistMerges(candidates []*mergeCandidate, shared map[int64]bool) []ArtistMerge {
	var merges []ArtistMerge
	assigned := map[int64]bool{}
	for _, local := range candidates {
		key := artistMergeKey(local.name)
		if local.identified || shared[local.id] || key == "" {
			continue
		}
		var matches []*mergeCandidate
		for _, other := range candidates {
			if other.identified && other.keys[key] {
				matches = append(matches, other)
			}
		}
		if len(matches) == 1 {
			merges = append(merges, ArtistMerge{
				LoserID: local.id, WinnerID: matches[0].id, LoserName: local.name, WinnerName: matches[0].name,
			})
			assigned[local.id] = true
		}
	}
	groups := map[string][]*mergeCandidate{}
	for _, local := range candidates {
		key := artistMergeKey(local.name)
		if !local.identified && !shared[local.id] && !assigned[local.id] && key != "" {
			groups[key] = append(groups[key], local)
		}
	}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		winner := group[0]
		for _, other := range group[1:] {
			if other.works > winner.works {
				winner = other
			}
		}
		for _, loser := range group {
			if loser != winner {
				merges = append(merges, ArtistMerge{
					LoserID: loser.id, WinnerID: winner.id, LoserName: loser.name, WinnerName: winner.name,
				})
			}
		}
	}
	slices.SortFunc(merges, func(a, b ArtistMerge) int { return int(a.LoserID - b.LoserID) })
	return merges
}

// artistMergeKey is a name with case and punctuation ignored, so "AD/XK", "AD XK" and "ac+dc" meet.
func artistMergeKey(name string) string {
	var key strings.Builder
	for _, character := range name {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			key.WriteRune(unicode.ToLower(character))
		}
	}
	return key.String()
}

// MergeArtists applies merges, one transaction each. The loser's name becomes an alias of the winner.
func (p People) MergeArtists(ctx context.Context, merges []ArtistMerge) error {
	for _, merge := range merges {
		err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			return mergeArtist(ctx, librarydb.New(tx), merge)
		})
		if err != nil {
			return fmt.Errorf("library: merge artist %d into %d: %w", merge.LoserID, merge.WinnerID, err)
		}
	}
	return nil
}

func mergeArtist(ctx context.Context, queries *librarydb.Queries, merge ArtistMerge) error {
	if err := queries.AddPersonAlias(ctx, librarydb.AddPersonAliasParams{
		PersonID: merge.WinnerID, Alias: merge.LoserName, Normalized: normalizedAlias(merge.LoserName), Source: "merge",
	}); err != nil {
		return err
	}
	winner, loser := merge.WinnerID, merge.LoserID
	steps := []func() error{
		func() error {
			return queries.MergeCreditsInto(ctx, librarydb.MergeCreditsIntoParams{WinnerID: winner, LoserID: loser})
		},
		func() error {
			return queries.MergeExternalIDsInto(ctx, librarydb.MergeExternalIDsIntoParams{WinnerID: winner, LoserID: loser})
		},
		func() error {
			return queries.MergeAliasesInto(ctx, librarydb.MergeAliasesIntoParams{WinnerID: winner, LoserID: loser})
		},
		func() error {
			return queries.MergeLeadPersonInto(ctx, librarydb.MergeLeadPersonIntoParams{
				WinnerID: sql.NullInt64{Int64: winner, Valid: true}, LoserID: sql.NullInt64{Int64: loser, Valid: true},
			})
		},
		func() error {
			return queries.MergeTagAssignmentsInto(ctx, librarydb.MergeTagAssignmentsIntoParams{WinnerID: winner, LoserID: loser})
		},
		func() error {
			return queries.MergeSubscriptionsInto(ctx, librarydb.MergeSubscriptionsIntoParams{WinnerID: winner, LoserID: loser})
		},
		func() error {
			return queries.MergeArtworkInto(ctx, librarydb.MergeArtworkIntoParams{WinnerID: winner, LoserID: loser})
		},
		func() error {
			return queries.MergeMonitoredInto(ctx, librarydb.MergeMonitoredIntoParams{WinnerID: winner, LoserID: loser})
		},
		func() error { return queries.DeletePersonTagAssignments(ctx, loser) },
		func() error { return queries.DeletePersonSubscriptions(ctx, loser) },
		func() error { return queries.DeletePersonArtwork(ctx, loser) },
		func() error { return queries.DeletePerson(ctx, loser) },
		func() error { return refreshPeople(ctx, queries, []int64{winner}) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// ProviderWikidata is the external-id provider of a person's Wikidata QID, the bridge between libraries.
const ProviderWikidata = "wikidata"

// musicbrainzArtistProvider mirrors music.ProviderArtist, which this package cannot import.
const musicbrainzArtistProvider = "musicbrainz:artist"

// LinkWikidata records a person's QID. When another person already holds it they are one human, so the two
// merge (the music artist survives: its folders are named by it) and the survivor's id is answered.
func (p People) LinkWikidata(ctx context.Context, personID int64, qid string) (int64, error) {
	qid = strings.ToUpper(strings.TrimSpace(qid))
	if qid == "" {
		return personID, nil
	}
	survivor := personID
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		survivor = personID
		owner, err := queries.GetPersonByExternalID(ctx, librarydb.GetPersonByExternalIDParams{Provider: ProviderWikidata, ExternalID: qid})
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case owner.ID != personID:
			merge, err := bridgeMerge(ctx, queries, owner, personID)
			if err != nil {
				return err
			}
			if err := mergeArtist(ctx, queries, merge); err != nil {
				return err
			}
			survivor = merge.WinnerID
		}
		return queries.LinkPersonExternalID(ctx, librarydb.LinkPersonExternalIDParams{
			Provider: ProviderWikidata, ExternalID: qid, PersonID: survivor,
		})
	})
	if err != nil {
		return personID, fmt.Errorf("library: link wikidata %s to person %d: %w", qid, personID, err)
	}
	return survivor, nil
}

func bridgeMerge(ctx context.Context, queries *librarydb.Queries, owner librarydb.Person, other int64) (ArtistMerge, error) {
	otherPerson, err := queries.GetPerson(ctx, other)
	if err != nil {
		return ArtistMerge{}, err
	}
	ownerIDs, err := queries.ListPersonExternalIDs(ctx, owner.ID)
	if err != nil {
		return ArtistMerge{}, err
	}
	winner, loser := owner, otherPerson
	for _, external := range ownerIDs {
		if external.Provider == musicbrainzArtistProvider {
			return ArtistMerge{WinnerID: winner.ID, LoserID: loser.ID, WinnerName: winner.Name, LoserName: loser.Name}, nil
		}
	}
	otherIDs, err := queries.ListPersonExternalIDs(ctx, other)
	if err != nil {
		return ArtistMerge{}, err
	}
	for _, external := range otherIDs {
		if external.Provider == musicbrainzArtistProvider {
			winner, loser = otherPerson, owner
		}
	}
	return ArtistMerge{WinnerID: winner.ID, LoserID: loser.ID, WinnerName: winner.Name, LoserName: loser.Name}, nil
}

// providerWikidataNone marks a person whose TMDB page names no Wikidata item, so the bridge sweep stops asking.
const providerWikidataNone = "wikidata:none"

// BridgeCandidate is a person known from TMDB who shares a name with a music artist, and so may be the same human.
type BridgeCandidate struct {
	PersonID int64
	TMDBID   string
}

// bridgeName folds a name to its letters and digits, so "'Weird Al' Yankovic" and "Weird Al Yankovic" meet; a name
// alone still never merges anyone, the Wikidata item does.
func bridgeName(name string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// artistNames is the folded name of every music artist, with whether the artist already has a Wikidata item.
func (p People) artistNames(ctx context.Context) (map[string]bool, error) {
	rows, err := p.store.Read().QueryContext(ctx,
		`SELECT m.name, EXISTS (SELECT 1 FROM person_external_id mw WHERE mw.person_id = m.id AND mw.provider = ?)
FROM person m JOIN person_external_id me ON me.person_id = m.id AND me.provider = ?`, ProviderWikidata, musicbrainzArtistProvider)
	if err != nil {
		return nil, fmt.Errorf("library: artist names: %w", err)
	}
	defer func() { _ = rows.Close() }()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		var hasWikidata bool
		if err := rows.Scan(&name, &hasWikidata); err != nil {
			return nil, fmt.Errorf("library: scan artist name: %w", err)
		}
		key := bridgeName(name)
		names[key] = names[key] || hasWikidata
	}
	return names, rows.Err()
}

// BridgeCandidates lists up to limit such people that have no Wikidata item recorded or ruled out yet.
func (p People) BridgeCandidates(ctx context.Context, limit int) ([]BridgeCandidate, error) {
	artists, err := p.artistNames(ctx)
	if err != nil || len(artists) == 0 {
		return nil, err
	}
	rows, err := p.store.Read().QueryContext(ctx,
		`SELECT t.id, t.name, x.external_id
FROM person t JOIN person_external_id x ON x.person_id = t.id AND x.provider = 'tmdb'
WHERE NOT EXISTS (SELECT 1 FROM person_external_id w WHERE w.person_id = t.id AND w.provider IN (?, ?, ?))
ORDER BY t.id`, ProviderWikidata, providerWikidataNone, musicbrainzArtistProvider)
	if err != nil {
		return nil, fmt.Errorf("library: bridge candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var candidates []BridgeCandidate
	for rows.Next() && len(candidates) < limit {
		var candidate BridgeCandidate
		var name string
		if err := rows.Scan(&candidate.PersonID, &name, &candidate.TMDBID); err != nil {
			return nil, fmt.Errorf("library: scan bridge candidate: %w", err)
		}
		if _, isArtistName := artists[bridgeName(name)]; isArtistName {
			candidates = append(candidates, candidate)
		}
	}
	return candidates, rows.Err()
}

// RuleOutWikidata records that no Wikidata item is known for the person.
func (p People) RuleOutWikidata(ctx context.Context, personID int64, tmdbID string) error {
	return p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).LinkPersonExternalID(ctx, librarydb.LinkPersonExternalIDParams{
			Provider: providerWikidataNone, ExternalID: tmdbID, PersonID: personID,
		})
	})
}

// BridgeLinked lists TV people whose Wikidata item is known but who still stand apart from the music artist of their
// name: the ones a Wikidata look-up of the item may yet join. Answers person id to item, up to limit.
func (p People) BridgeLinked(ctx context.Context, limit int) (map[int64]string, error) {
	artists, err := p.artistNames(ctx)
	if err != nil || len(artists) == 0 {
		return nil, err
	}
	rows, err := p.store.Read().QueryContext(ctx,
		`SELECT t.id, t.name, w.external_id
FROM person t JOIN person_external_id x ON x.person_id = t.id AND x.provider = 'tmdb'
JOIN person_external_id w ON w.person_id = t.id AND w.provider = ?
WHERE NOT EXISTS (SELECT 1 FROM person_external_id me WHERE me.person_id = t.id AND me.provider = ?)
ORDER BY t.id`, ProviderWikidata, musicbrainzArtistProvider)
	if err != nil {
		return nil, fmt.Errorf("library: bridge linked: %w", err)
	}
	defer func() { _ = rows.Close() }()
	linked := map[int64]string{}
	for rows.Next() && len(linked) < limit {
		var id int64
		var name, qid string
		if err := rows.Scan(&id, &name, &qid); err != nil {
			return nil, fmt.Errorf("library: scan bridge linked: %w", err)
		}
		// An artist that already has an item is merged by it, not by a name.
		if hasWikidata, isArtistName := artists[bridgeName(name)]; isArtistName && !hasWikidata {
			linked[id] = qid
		}
	}
	return linked, rows.Err()
}

// duplicate is one identified person as the convergence pass sees them.
type duplicate struct {
	id, credits int64
	name, born  string
	providers   map[string]bool
}

// adult is true when the person holds an adult library's performer id.
func (d *duplicate) adult() bool {
	for provider := range d.providers {
		if isAdultProvider(provider) {
			return true
		}
	}
	return false
}

// maxConvergePasses bounds the rounds of one run; a merge can make another one uncontested.
const maxConvergePasses = 3

// ConvergePeople merges the people two sources made of one human. Of the identified people sharing a name (or one's
// alias being the other's name) all merge when they hold no provider twice and no two births differ; where that does
// not hold, only those of one known birth merge, and the rest stay apart. It answers how many people were merged.
func (p People) ConvergePeople(ctx context.Context) (int, error) {
	if err := p.SplitAdultPeople(ctx); err != nil {
		return 0, err
	}
	total := 0
	for range maxConvergePasses {
		merged, err := p.convergeOnce(ctx)
		total += merged
		if err != nil || merged == 0 {
			return total, err
		}
	}
	return total, nil
}

func (p People) convergeOnce(ctx context.Context) (int, error) {
	groups, err := p.duplicateGroups(ctx)
	if err != nil {
		return 0, err
	}
	merged := 0
	gone := map[int64]bool{}
	for _, group := range groups {
		for _, cluster := range uncontested(group) {
			winner := cluster[0]
			for _, person := range cluster[1:] {
				if survives(person, winner) {
					winner = person
				}
			}
			for _, person := range cluster {
				if person == winner || gone[person.id] || gone[winner.id] {
					continue
				}
				if err := ctx.Err(); err != nil {
					return merged, err
				}
				if err := p.MergePeople(ctx, person.id, winner.id); err != nil {
					return merged, err
				}
				gone[person.id] = true
				merged++
			}
		}
	}
	return merged, nil
}

// survives is true when a is the better one to keep than b: the music artist (its folders are named by it), then the
// one with more credits, then the older.
func survives(a, b *duplicate) bool {
	if a.providers[musicbrainzArtistProvider] != b.providers[musicbrainzArtistProvider] {
		return a.providers[musicbrainzArtistProvider]
	}
	return a.credits > b.credits
}

// uncontested splits one name's people into the sets that are one human each: everyone when nothing contradicts, else
// those of one birth.
func uncontested(group []*duplicate) [][]*duplicate {
	if len(group) < 2 {
		return nil
	}
	if compatibleAll(group) {
		return [][]*duplicate{group}
	}
	var clusters [][]*duplicate
	for _, person := range group {
		if person.born == "" {
			continue
		}
		placed := false
		for index, cluster := range clusters {
			if compatibleAll(append(slices.Clone(cluster), person)) {
				clusters[index] = append(cluster, person)
				placed = true
				break
			}
		}
		if !placed {
			clusters = append(clusters, []*duplicate{person})
		}
	}
	return slices.DeleteFunc(clusters, func(cluster []*duplicate) bool { return len(cluster) < 2 })
}

// compatibleAll is true when no provider is held twice and no two births contradict.
func compatibleAll(people []*duplicate) bool {
	seen := map[string]bool{}
	for index, person := range people {
		if person.adult() != people[0].adult() {
			return false
		}
		for provider := range person.providers {
			if seen[provider] {
				return false
			}
			seen[provider] = true
		}
		for _, other := range people[:index] {
			if !bornCompatible(person.born, other.born) {
				return false
			}
		}
	}
	return true
}

// duplicateGroups lists the identified people by lower-cased name; a person is also in the group of each alias that
// names another group's people.
func (p People) duplicateGroups(ctx context.Context) (map[string][]*duplicate, error) {
	rows, err := p.store.Read().QueryContext(ctx, `SELECT p.id, p.name, COALESCE(p.born, ''), x.provider,
       (SELECT COUNT(*) FROM credit c WHERE c.person_id = p.id)
FROM person p JOIN person_external_id x ON x.person_id = p.id
WHERE x.provider NOT LIKE 'local:%' AND TRIM(p.name) <> ''
ORDER BY p.id`)
	if err != nil {
		return nil, fmt.Errorf("library: list identified people: %w", err)
	}
	defer func() { _ = rows.Close() }()
	groups := map[string][]*duplicate{}
	byID := map[int64]*duplicate{}
	for rows.Next() {
		var id, credits int64
		var name, born, provider string
		if err := rows.Scan(&id, &name, &born, &provider, &credits); err != nil {
			return nil, err
		}
		person, seen := byID[id]
		if !seen {
			person = &duplicate{id: id, name: name, born: born, credits: credits, providers: map[string]bool{}}
			byID[id] = person
			key := normalizedAlias(name)
			groups[key] = append(groups[key], person)
		}
		person.providers[provider] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	aliases, err := p.store.Read().QueryContext(ctx, `SELECT person_id, normalized FROM person_alias ORDER BY person_id`)
	if err != nil {
		return nil, fmt.Errorf("library: list aliases: %w", err)
	}
	defer func() { _ = aliases.Close() }()
	for aliases.Next() {
		var id int64
		var alias string
		if err := aliases.Scan(&id, &alias); err != nil {
			return nil, err
		}
		person, identified := byID[id]
		if _, named := groups[alias]; !identified || !named || alias == normalizedAlias(person.name) || slices.Contains(groups[alias], person) {
			continue
		}
		groups[alias] = append(groups[alias], person)
	}
	return groups, aliases.Err()
}

// SplitAdultPeople undoes name-only joins of an adult performer with a person of another domain (a music artist, an
// actor): the performer ids and the credits on adult works move to a person of their own.
func (p People) SplitAdultPeople(ctx context.Context) error {
	mixed, err := p.mixedPeople(ctx)
	if err != nil {
		return err
	}
	for chunk := range slices.Chunk(mixed, 50) {
		err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			for _, id := range chunk {
				if err := splitAdultPerson(ctx, tx, id); err != nil {
					return fmt.Errorf("split person %d: %w", id, err)
				}
			}
			return refreshPeople(ctx, librarydb.New(tx), chunk)
		})
		if err != nil {
			return fmt.Errorf("library: %w", err)
		}
	}
	return nil
}

func splitAdultPerson(ctx context.Context, tx *sql.Tx, id int64) error {
	created, err := tx.ExecContext(ctx, `INSERT INTO person (name, sort_name, disambiguation, kind, gender, country, born, died, fields_json, monitored, added_at, name_locked)
SELECT name, sort_name, disambiguation, kind, gender, country, born, died, fields_json, 0, added_at, name_locked FROM person WHERE id = ?`, id)
	if err != nil {
		return err
	}
	adultID, err := created.LastInsertId()
	if err != nil {
		return err
	}
	moves := []struct {
		statement string
		args      []any
	}{
		{`UPDATE person_external_id SET person_id = ? WHERE person_id = ? AND provider LIKE '%:performer'`, []any{adultID, id}},
		{`UPDATE credit SET person_id = ? WHERE person_id = ? AND work_id IN (SELECT id FROM work WHERE media_type = 'adult')`, []any{adultID, id}},
		{`UPDATE person SET fields_json = json_remove(fields_json, '$.image_url')
WHERE id = ? AND (json_extract(fields_json, '$.image_url') LIKE '%stashdb%' OR json_extract(fields_json, '$.image_url') LIKE '%porndb%' OR json_extract(fields_json, '$.image_url') LIKE '%fansdb%')`, []any{id}},
	}
	for _, move := range moves {
		if _, err := tx.ExecContext(ctx, move.statement, move.args...); err != nil {
			return err
		}
	}
	return refreshPeople(ctx, librarydb.New(tx), []int64{adultID})
}

// mixedPeople lists the people who hold an adult performer id and an id of another domain.
func (p People) mixedPeople(ctx context.Context) ([]int64, error) {
	rows, err := p.store.Read().QueryContext(ctx, `SELECT p.id FROM person p
WHERE EXISTS (SELECT 1 FROM person_external_id x WHERE x.person_id = p.id AND x.provider LIKE '%:performer')
  AND EXISTS (SELECT 1 FROM person_external_id y WHERE y.person_id = p.id AND y.provider NOT LIKE '%:performer' AND y.provider NOT LIKE 'local:%')
ORDER BY p.id`)
	if err != nil {
		return nil, fmt.Errorf("library: list mixed people: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var mixed []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		mixed = append(mixed, id)
	}
	return mixed, rows.Err()
}
