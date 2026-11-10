package franchiselink

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/db/library"
)

// ProviderKeyword is the namespace of a franchise key a provider's keyword gives ("keyword:<name>").
const ProviderKeyword = "keyword"

// keywordKind is the classification a provider's keywords are stored under.
const keywordKind = "keyword"

// ProviderList is the namespace of a franchise key a provider's official list gives ("list:tvdb:<id>").
const ProviderList = "list"

// KeywordStore is what the keyword step reads of a media type's collections and declares on them.
type KeywordStore interface {
	Summaries(ctx context.Context, mediaType string) ([]library.FranchiseSummary, error)
	FranchiseMemberships(ctx context.Context, mediaType string) ([]library.Membership, error)
	Classifications(ctx context.Context, mediaType, kind string) (map[int64][]string, error)
	DeclareKeys(ctx context.Context, collectionID int64, provider string, externalIDs []string) error
}

// declareKeys gives each collection of the media types the keyword key its own name is, when most of its works carry that
// keyword (TMDB tags a franchise's films and shows alike), and the key of each official list most of its works are on.
func (s Service) declareKeys(ctx context.Context, mediaTypes []string) error {
	if s.Keywords == nil {
		return nil
	}
	for _, mediaType := range mediaTypes {
		summaries, err := s.Keywords.Summaries(ctx, mediaType)
		if err != nil {
			return err
		}
		memberships, err := s.Keywords.FranchiseMemberships(ctx, mediaType)
		if err != nil {
			return err
		}
		keywords, err := s.Keywords.Classifications(ctx, mediaType, keywordKind)
		if err != nil {
			return err
		}
		lists, err := s.Keywords.Classifications(ctx, mediaType, library.FranchiseListKind)
		if err != nil {
			return err
		}
		members := map[int64][]int64{}
		for _, membership := range memberships {
			members[membership.CollectionID] = append(members[membership.CollectionID], membership.WorkID)
		}
		for _, summary := range summaries {
			var keys []string
			if key := keywordKey(summary.Name, members[summary.ID], keywords); key != "" {
				keys = []string{key}
			}
			if err := s.Keywords.DeclareKeys(ctx, summary.ID, ProviderKeyword, keys); err != nil {
				return err
			}
			if err := s.Keywords.DeclareKeys(ctx, summary.ID, ProviderList, mostlyOn(members[summary.ID], lists)); err != nil {
				return err
			}
		}
	}
	return nil
}

// keywordKey is the collection's own name as a keyword more than half its works carry, "" when they do not: a keyword
// alone ("superhero", "sequel") says nothing of a franchise, one that is the collection's name does.
func keywordKey(name string, members []int64, keywords map[int64][]string) string {
	wanted := franchiseName(name)
	if wanted == "" || len(members) == 0 {
		return ""
	}
	carrying := 0
	for _, workID := range members {
		if slices.ContainsFunc(keywords[workID], func(keyword string) bool { return franchiseName(keyword) == wanted }) {
			carrying++
		}
	}
	if carrying*2 <= len(members) {
		return ""
	}
	return wanted
}

// franchiseName is a collection or keyword name as plain lower-case words, without TMDB's " Collection" or a leading "the".
func franchiseName(name string) string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) > 1 && words[len(words)-1] == "collection" {
		words = words[:len(words)-1]
	}
	if len(words) > 1 && words[0] == "the" {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// mostlyOn are the lists more than half a collection's works are on, sorted: one series of a loose family on a list says
// nothing of the rest, a film collection whose films a franchise list names is that franchise's.
func mostlyOn(members []int64, lists map[int64][]string) []string {
	counts := map[string]int{}
	for _, workID := range members {
		for _, list := range lists[workID] {
			counts[list]++
		}
	}
	var on []string
	for list, carrying := range counts {
		if carrying*2 > len(members) {
			on = append(on, list)
		}
	}
	slices.Sort(on)
	return on
}
