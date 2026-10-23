package library_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

var musicTree = []library.TaxonomyTag{
	{Name: "Rock", Children: []library.TaxonomyTag{{Name: "Metal"}}},
	{Name: "Hip Hop", Aliases: []string{"rap", "hip hop music"}},
}

// tagPaths renders a taxonomy's tree as "Parent/Child" paths with their work counts, sorted.
func tagPaths(t *testing.T, people library.People, taxonomy string) []string {
	t.Helper()
	tree, err := people.TagTree(t.Context(), taxonomy)
	if err != nil {
		t.Fatalf("TagTree: %v", err)
	}
	byID := map[int64]library.TagNode{}
	for _, node := range tree {
		byID[node.ID] = node
	}
	var paths []string
	for _, node := range tree {
		path := node.Name
		for parent := node.ParentID; parent != 0; parent = byID[parent].ParentID {
			path = byID[parent].Name + "/" + path
		}
		paths = append(paths, path+" "+strconv.FormatInt(node.Works, 10))
	}
	slices.Sort(paths)
	return paths
}

// The shipped tree takes over a provider's tag of the same name, places the rest by the genre their name ends with
// and is applied once per version.
func TestSeedTaxonomy_PlacesProviderGenresUnderTheShippedTree(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	work := createWork(t, opened, "music", "Alpha")
	if err := people.AssignGenres(t.Context(), work.ID, "music", []library.GenreLink{
		{Name: "rock"}, {Name: "alternative metal"}, {Name: "polka"}, {Name: "hip hop music"},
	}); err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}

	if err := people.SeedTaxonomy(t.Context(), "music", 1, musicTree); err != nil {
		t.Fatalf("SeedTaxonomy: %v", err)
	}
	want := []string{"Hip Hop 1", "Polka 1", "Rock 1", "Rock/Metal 0", "Rock/Metal/Alternative Metal 1"}
	if got := tagPaths(t, people, "music"); !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %v\nwant %v", got, want)
	}

	if err := people.SeedTaxonomy(t.Context(), "music", 1, []library.TaxonomyTag{{Name: "Polka"}}); err != nil {
		t.Fatalf("SeedTaxonomy again: %v", err)
	}
	if got := tagPaths(t, people, "music"); !reflect.DeepEqual(got, want) {
		t.Fatalf("the same version applied twice: tree = %v", got)
	}

	if err := people.AssignGenres(t.Context(), work.ID, "music", []library.GenreLink{{Name: "Rap"}, {Name: "gangsta rap"}}); err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}
	want = []string{"Hip Hop 1", "Hip Hop/Gangsta Rap 1", "Polka 0", "Rock 0", "Rock/Metal 0", "Rock/Metal/Alternative Metal 0"}
	if got := tagPaths(t, people, "music"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after an alias and a genre ending with one: tree = %v\nwant %v", got, want)
	}
}

// A provider's genre renamed keeps its old name as an alias; a move refuses another taxonomy and its own subtree; a
// merge carries works and the merged name; a delete moves the children up.
func TestTagTree_EditMergeDelete(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	if err := people.SeedTaxonomy(t.Context(), "music", 1, musicTree); err != nil {
		t.Fatalf("SeedTaxonomy: %v", err)
	}
	work := createWork(t, opened, "music", "Alpha")
	if err := people.AssignGenres(t.Context(), work.ID, "music", []library.GenreLink{{Name: "thrash"}}); err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}
	ids := tagIDs(t, people)

	if err := people.EditTag(t.Context(), ids["Thrash"], "Thrash Metal", ids["Metal"]); err != nil {
		t.Fatalf("EditTag: %v", err)
	}
	if err := people.AssignGenres(t.Context(), work.ID, "music", []library.GenreLink{{Name: "thrash"}}); err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}
	want := []string{"Hip Hop 0", "Rock 0", "Rock/Metal 0", "Rock/Metal/Thrash Metal 1"}
	if got := tagPaths(t, people, "music"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after renaming a genre the provider names again: tree = %v\nwant %v", got, want)
	}

	free, err := people.CreateTag(t.Context(), "Road trip", "", 0)
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	for name, move := range map[string]func() error{
		"under another taxonomy":   func() error { return people.EditTag(t.Context(), ids["Rock"], "Rock", free) },
		"under its own descendant": func() error { return people.EditTag(t.Context(), ids["Rock"], "Rock", ids["Metal"]) },
		"into its own descendant":  func() error { return people.MergeTag(t.Context(), ids["Rock"], ids["Metal"]) },
	} {
		if err := move(); !errors.Is(err, library.ErrTagMove) {
			t.Errorf("%s: %v, want ErrTagMove", name, err)
		}
	}
	if _, err := people.CreateTag(t.Context(), "road  TRIP", "", 0); !errors.Is(err, library.ErrTagExists) {
		t.Errorf("a second Road trip: %v, want ErrTagExists", err)
	}

	if err := people.MergeTag(t.Context(), ids["Metal"], ids["Hip Hop"]); err != nil {
		t.Fatalf("MergeTag: %v", err)
	}
	want = []string{"Hip Hop 0", "Hip Hop/Thrash Metal 1", "Rock 0"}
	if got := tagPaths(t, people, "music"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after merging Metal into Hip Hop: tree = %v\nwant %v", got, want)
	}
	if err := people.AssignGenres(t.Context(), work.ID, "music", []library.GenreLink{{Name: "metal"}}); err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}
	if got := tagPaths(t, people, "music"); !slices.Contains(got, "Hip Hop 1") {
		t.Fatalf("the merged name lands on the kept tag: tree = %v", got)
	}

	if err := people.DeleteTag(t.Context(), ids["Hip Hop"]); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	if got, want := tagPaths(t, people, "music"), []string{"Rock 0", "Thrash Metal 0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after deleting Hip Hop: tree = %v, want %v", got, want)
	}
}

func tagIDs(t *testing.T, people library.People) map[string]int64 {
	t.Helper()
	tree, err := people.TagTree(t.Context(), "*")
	if err != nil {
		t.Fatalf("TagTree: %v", err)
	}
	ids := map[string]int64{}
	for _, node := range tree {
		ids[node.Name] = node.ID
	}
	return ids
}

// Genres stored before they were tags move into the work's type's taxonomy, aliases splitting a combined one.
func TestConvertClassificationGenres_MovesThemIntoTheTaxonomy(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	if err := people.SeedTaxonomy(t.Context(), "screen", 1, []library.TaxonomyTag{
		{Name: "Action", Aliases: []string{"Action & Adventure"}}, {Name: "Adventure", Aliases: []string{"Action & Adventure"}},
	}); err != nil {
		t.Fatalf("SeedTaxonomy: %v", err)
	}
	series := createWork(t, opened, "tv", "Show")
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, genre := range []string{"Action & Adventure", "Drama"} {
			if err := q.SetWorkClassification(ctx, librarydb.SetWorkClassificationParams{WorkID: series.ID, Kind: "genre", Value: genre, Source: "tmdb"}); err != nil {
				return err
			}
		}
		return nil
	})

	converted, err := people.ConvertClassificationGenres(t.Context(), func(mediaType string) string {
		return map[string]string{"tv": "screen"}[mediaType]
	})
	if err != nil || converted != 1 {
		t.Fatalf("ConvertClassificationGenres = %d, %v; want 1 work", converted, err)
	}
	if got, want := tagPaths(t, people, "screen"), []string{"Action 1", "Adventure 1", "Drama 1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	if again, err := people.ConvertClassificationGenres(t.Context(), func(string) string { return "screen" }); err != nil || again != 0 {
		t.Fatalf("a second run converted %d, %v; want nothing left", again, err)
	}
}

// A chip matches a work carrying the tag or one below it; a negated chip leaves such works out.
func TestWorksList_FiltersByTagChipsWithTheirSubtree(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	if err := people.SeedTaxonomy(t.Context(), "music", 1, musicTree); err != nil {
		t.Fatalf("SeedTaxonomy: %v", err)
	}
	metal, rap, _ := createWork(t, opened, "music", "Metal album"), createWork(t, opened, "music", "Rap album"), createWork(t, opened, "music", "Plain album")
	for work, genre := range map[int64]string{metal.ID: "Metal", rap.ID: "rap"} {
		if err := people.AssignGenres(t.Context(), work, "music", []library.GenreLink{{Name: genre}}); err != nil {
			t.Fatalf("AssignGenres: %v", err)
		}
	}
	ids := tagIDs(t, people)
	titles := func(filter library.WorkFilter) []string {
		rows, err := library.NewWorks(opened).List(t.Context(), filter, library.WorkCursor{}, 10)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		var got []string
		for _, row := range rows {
			got = append(got, row.CanonicalTitle)
		}
		return got
	}
	if got, want := titles(library.WorkFilter{TagIDs: []int64{ids["Rock"]}}), []string{"Metal album"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Rock = %v, want %v (Metal is below it)", got, want)
	}
	if got, want := titles(library.WorkFilter{ExcludedTagIDs: []int64{ids["Rock"]}}), []string{"Plain album", "Rap album"}; !reflect.DeepEqual(got, want) {
		t.Errorf("not Rock = %v, want %v", got, want)
	}
	if got := titles(library.WorkFilter{TagIDs: []int64{ids["Rock"], ids["Hip Hop"]}}); len(got) != 0 {
		t.Errorf("Rock and Hip Hop = %v, want none: chips must all hold", got)
	}
}
