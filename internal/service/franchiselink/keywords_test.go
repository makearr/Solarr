package franchiselink

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

type fakeKeywords struct {
	summaries   map[string][]library.FranchiseSummary
	memberships map[string][]library.Membership
	keywords    map[string]map[int64][]string
	lists       map[string]map[int64][]string
	declared    map[int64]map[string][]string
	// store is told of every declaration, so the links that follow see it.
	store *fakeStore
}

func (f *fakeKeywords) Summaries(_ context.Context, mediaType string) ([]library.FranchiseSummary, error) {
	return f.summaries[mediaType], nil
}

func (f *fakeKeywords) FranchiseMemberships(_ context.Context, mediaType string) ([]library.Membership, error) {
	return f.memberships[mediaType], nil
}

func (f *fakeKeywords) Classifications(_ context.Context, mediaType, kind string) (map[int64][]string, error) {
	if kind == library.FranchiseListKind {
		return f.lists[mediaType], nil
	}
	return f.keywords[mediaType], nil
}

func (f *fakeKeywords) DeclareKeys(_ context.Context, collectionID int64, provider string, externalIDs []string) error {
	if f.declared[collectionID] == nil {
		f.declared[collectionID] = map[string][]string{}
	}
	f.declared[collectionID][provider] = externalIDs
	for index := range f.store.candidates {
		if f.store.candidates[index].ID == collectionID {
			for _, id := range externalIDs {
				f.store.candidates[index].Keys = append(f.store.candidates[index].Keys, provider+":"+id)
			}
		}
	}
	return nil
}

func TestLink_DeclaresTheCollectionsNameAsAKeywordMostOfItsWorksCarryAndLinksByIt(t *testing.T) {
	store := &fakeStore{candidates: []library.LinkCandidate{
		{ID: 1, MediaType: "movie", Members: 3, Keys: []string{"tmdb:collection:10"}, Identity: []string{"tmdb:collection:10"}},
		{ID: 2, MediaType: "tv", Members: 2, Keys: []string{"auto:2"}, Identity: []string{"auto:2"}},
		{ID: 3, MediaType: "tv", Members: 2, Keys: []string{"auto:3"}, Identity: []string{"auto:3"}},
	}, refused: map[[2]string]bool{}}
	keywords := &fakeKeywords{
		summaries: map[string][]library.FranchiseSummary{
			"movie": {{ID: 1, Name: "The Vexmoor Collection"}},
			"tv":    {{ID: 2, Name: "Vexmoor"}, {ID: 3, Name: "Brightwater"}},
		},
		memberships: map[string][]library.Membership{
			"movie": {{CollectionID: 1, WorkID: 11}, {CollectionID: 1, WorkID: 12}, {CollectionID: 1, WorkID: 13}},
			"tv":    {{CollectionID: 2, WorkID: 21}, {CollectionID: 2, WorkID: 22}, {CollectionID: 3, WorkID: 31}, {CollectionID: 3, WorkID: 32}},
		},
		keywords: map[string]map[int64][]string{
			"movie": {11: {"Vexmoor", "space opera"}, 12: {"vexmoor"}, 13: {"space opera"}},
			// One of Brightwater's two shows carrying its name is not most of them; "space opera" is no collection's name.
			"tv": {21: {"vexmoor"}, 22: {"vexmoor"}, 31: {"brightwater"}, 32: {"space opera"}},
		},
		declared: map[int64]map[string][]string{},
		store:    store,
	}
	service := Service{Store: store, Keywords: keywords, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	made, err := service.Link(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	declared := map[int64]map[string][]string{
		1: {ProviderKeyword: {"vexmoor"}, ProviderList: nil}, 2: {ProviderKeyword: {"vexmoor"}, ProviderList: nil}, 3: {ProviderKeyword: nil, ProviderList: nil},
	}
	if !reflect.DeepEqual(keywords.declared, declared) {
		t.Errorf("declared = %v, want %v", keywords.declared, declared)
	}
	want := []library.PlannedLink{{A: 1, B: 2, Reason: library.LinkReasonProvider, Detail: "keyword:vexmoor"}}
	if made != 1 || !reflect.DeepEqual(store.made, want) {
		t.Errorf("linked %d: %+v, want %+v", made, store.made, want)
	}
}

func TestLink_DeclaresTheOfficialListMostOfACollectionsWorksAreOnAndLinksByIt(t *testing.T) {
	store := &fakeStore{candidates: []library.LinkCandidate{
		{ID: 1, MediaType: "movie", Members: 2, Keys: []string{"tmdb:collection:10"}, Identity: []string{"tmdb:collection:10"}},
		{ID: 2, MediaType: "tv", Members: 2, Keys: []string{"auto:2"}, Identity: []string{"auto:2"}},
		{ID: 3, MediaType: "tv", Members: 3, Keys: []string{"auto:3"}, Identity: []string{"auto:3"}},
	}, refused: map[[2]string]bool{}}
	keywords := &fakeKeywords{
		summaries: map[string][]library.FranchiseSummary{
			"movie": {{ID: 1, Name: "Glimmerton Collection"}},
			"tv":    {{ID: 2, Name: "Glimmerton"}, {ID: 3, Name: "Harbour Nights"}},
		},
		memberships: map[string][]library.Membership{
			"movie": {{CollectionID: 1, WorkID: 11}, {CollectionID: 1, WorkID: 12}},
			"tv":    {{CollectionID: 2, WorkID: 21}, {CollectionID: 2, WorkID: 22}, {CollectionID: 3, WorkID: 31}, {CollectionID: 3, WorkID: 32}, {CollectionID: 3, WorkID: 33}},
		},
		lists: map[string]map[int64][]string{
			"movie": {11: {"tvdb:500"}, 12: {"tvdb:500", "tvdb:700"}},
			// One of Harbour Nights' three shows on list 500 is not most of them.
			"tv": {21: {"tvdb:500"}, 22: {"tvdb:500"}, 31: {"tvdb:500"}},
		},
		declared: map[int64]map[string][]string{},
		store:    store,
	}
	service := Service{Store: store, Keywords: keywords, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	made, err := service.Link(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]map[string][]string{
		1: {ProviderKeyword: nil, ProviderList: {"tvdb:500"}}, 2: {ProviderKeyword: nil, ProviderList: {"tvdb:500"}}, 3: {ProviderKeyword: nil, ProviderList: nil},
	}
	if !reflect.DeepEqual(keywords.declared, want) {
		t.Errorf("declared = %v, want %v", keywords.declared, want)
	}
	links := []library.PlannedLink{{A: 1, B: 2, Reason: library.LinkReasonProvider, Detail: "list:tvdb:500"}}
	if made != 1 || !reflect.DeepEqual(store.made, links) {
		t.Errorf("linked %d: %+v, want %+v", made, store.made, links)
	}
}

func TestFranchiseNameDropsTMDBsCollectionWordAndALeadingThe(t *testing.T) {
	for name, want := range map[string]string{
		"The Vexmoor Collection": "vexmoor", "Vexmoor: Rising": "vexmoor rising", "Collection": "collection", "The": "the", "": "",
	} {
		if got := franchiseName(name); got != want {
			t.Errorf("franchiseName(%q) = %q, want %q", name, got, want)
		}
	}
}
