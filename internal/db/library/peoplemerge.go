package library

import (
	"context"
	"database/sql"
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
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
