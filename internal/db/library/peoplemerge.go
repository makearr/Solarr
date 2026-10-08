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
			return queries.MergePrimaryArtistInto(ctx, librarydb.MergePrimaryArtistIntoParams{
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
