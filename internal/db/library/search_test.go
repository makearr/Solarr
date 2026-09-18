package library_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func searchOne(t *testing.T, results []library.SearchResult) librarydb.Work {
	t.Helper()
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(results), results)
	}
	return results[0].Work
}

func TestSearch_MatchesCanonicalTitle(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")

	results, err := library.NewSearch(opened).Works(t.Context(), "tender", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, results); found.ID != work.ID {
		t.Errorf("matched work %d, want %d", found.ID, work.ID)
	}
}

func TestSearch_MatchesAnAlias(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "tv", "Nathan for You")
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
			WorkID: work.ID, Title: "Nathan fur dich", Normalized: "nathan fur dich", Kind: "alias",
		})
		return err
	})

	results, err := library.NewSearch(opened).Works(t.Context(), "fur dich", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, results); found.ID != work.ID {
		t.Errorf("matched work %d, want %d", found.ID, work.ID)
	}
}

func TestSearch_MatchesAPartTitle(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
			WorkID: work.ID, MediaRef: "1:1", SortKey: "0001",
			Title: sql.NullString{String: "The Furab Keke", Valid: true}, Monitored: 1,
		})
		return err
	})

	results, err := library.NewSearch(opened).Works(t.Context(), "mercy seat", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, results); found.ID != work.ID {
		t.Errorf("matched work %d, want %d", found.ID, work.ID)
	}
}

func TestSearch_MatchesAPartAltTitle(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "tv", "Nathan for You")

	var partID int64
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		part, err := q.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
			WorkID: work.ID, MediaRef: "s01e01", SortKey: "0001",
			Title: sql.NullString{String: "Smokers Allowed", Valid: true}, Monitored: 1,
		})
		partID = part.ID
		return err
	})
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.AddWorkPartTitle(ctx, librarydb.AddWorkPartTitleParams{
			PartID: partID, Title: "Raucher erlaubt", Normalized: "raucher erlaubt",
		})
		return err
	})

	results, err := library.NewSearch(opened).Works(t.Context(), "raucher", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, results); found.ID != work.ID {
		t.Errorf("matched work %d, want %d", found.ID, work.ID)
	}
}

// The 250ms debounce (section 21.5) implies search-as-you-type: the last word of a
// still-being-typed query must match as a prefix, not only a complete word.
func TestSearch_MatchesAsYouType(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")

	results, err := library.NewSearch(opened).Works(t.Context(), "tender pr", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, results); found.ID != work.ID {
		t.Errorf("matched work %d, want %d", found.ID, work.ID)
	}
}

func TestSearch_FiltersByMediaType(t *testing.T) {
	opened, _ := newLibrary(t)
	createWork(t, opened, "music", "Push the Sky Away")
	movie := createWork(t, opened, "movie", "Push the Sky Away Documentary")

	results, err := library.NewSearch(opened).Works(t.Context(), "push the sky", "movie", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, results); found.ID != movie.ID {
		t.Errorf("matched work %d, want the movie %d", found.ID, movie.ID)
	}
}

// A work with several matching titles must still appear once: a common alias should
// never crowd another work's single result out of a small page.
func TestSearch_DeduplicatesAWorkWithSeveralMatchingTitles(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "tv", "Nathan for You")
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
			WorkID: work.ID, Title: "Nathan the show", Normalized: "nathan the show", Kind: "alias",
		})
		return err
	})

	results, err := library.NewSearch(opened).Works(t.Context(), "nathan", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	searchOne(t, results)
}

// A canonical-title match should outrank a work that only matches through a
// lower-signal alias, so the most relevant result leads a short results list.
func TestSearch_RanksTheCanonicalTitleMatchFirst(t *testing.T) {
	opened, _ := newLibrary(t)
	direct := createWork(t, opened, "music", "Boatman's Call")
	indirect := createWork(t, opened, "music", "Something Else Entirely")
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
			WorkID: indirect.ID, Title: "also known as Boatman's Call reissue",
			Normalized: "also known as boatmans call reissue", Kind: "alias",
		})
		return err
	})

	results, err := library.NewSearch(opened).Works(t.Context(), "boatman's call", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(results), results)
	}
	if results[0].Work.ID != direct.ID {
		t.Errorf("first result is work %d, want the direct title match %d",
			results[0].Work.ID, direct.ID)
	}
}

// The AFTER UPDATE trigger, not just AFTER INSERT: a renamed work must be found under
// its new title and not its old one, without anything re-indexing it by hand.
func TestSearch_TracksARenamedWork(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "movie", "Working Title")

	err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE work SET canonical_title = ? WHERE id = ?",
			"Final Title", work.ID)
		return err
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}

	renamed, err := library.NewSearch(opened).Works(t.Context(), "final title", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, renamed); found.ID != work.ID {
		t.Errorf("matched work %d, want %d", found.ID, work.ID)
	}

	stale, err := library.NewSearch(opened).Works(t.Context(), "working title", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("the old title still matches: %+v", stale)
	}
}

// The AFTER DELETE trigger on work: a deleted work must stop being found, not linger
// as a search result for something that no longer exists.
func TestSearch_ForgetsADeletedWork(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Briefly Here")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.DeleteWork(ctx, work.ID)
	})
	_ = queries

	results, err := library.NewSearch(opened).Works(t.Context(), "briefly here", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("the deleted work still matches: %+v", results)
	}
}

// The AFTER DELETE trigger on work_title specifically: removing one alias must not
// touch the work's own canonical-title entry.
func TestSearch_ForgetsARemovedAliasWithoutLosingTheWork(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "tv", "Kept Title")

	var aliasID int64
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
			WorkID: work.ID, Title: "Removable Alias", Normalized: "removable alias", Kind: "alias",
		})
		aliasID = row.ID
		return err
	})

	err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM work_title WHERE id = ?", aliasID)
		return err
	})
	if err != nil {
		t.Fatalf("delete alias: %v", err)
	}

	byAlias, err := library.NewSearch(opened).Works(t.Context(), "removable alias", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if len(byAlias) != 0 {
		t.Errorf("the removed alias still matches: %+v", byAlias)
	}

	byCanonical, err := library.NewSearch(opened).Works(t.Context(), "kept title", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, byCanonical); found.ID != work.ID {
		t.Errorf("matched work %d, want %d", found.ID, work.ID)
	}
}

func TestSearch_RejectsAQueryBelowTheMinimumLength(t *testing.T) {
	opened, _ := newLibrary(t)
	createWork(t, opened, "music", "A")

	for _, query := range []string{"", " ", "a"} {
		_, err := library.NewSearch(opened).Works(t.Context(), query, "", nil, nil, 10)
		if !errors.Is(err, library.ErrQueryTooShort) {
			t.Errorf("query %q: got %v, want ErrQueryTooShort", query, err)
		}
	}
}

// FTS5 gives AND, OR, NOT, "-token" negation, column filters and unmatched quotes
// special meaning. A title or a search box is free-text, so any of these appearing in
// either must search as plain characters rather than reach SQLite as query syntax --
// mutation-tested by removing the escaping in buildMatchQuery and confirming this
// fails with a syntax error rather than zero results.
func TestSearch_TreatsFTS5OperatorCharactersAsPlainText(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", `XT/QK: "Greatest" Hits - Live NOT Studio`)

	results, err := library.NewSearch(opened).Works(t.Context(),
		`XT/QK: "Greatest" Hits - Live NOT Studio`, "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, results); found.ID != work.ID {
		t.Errorf("matched work %d, want %d", found.ID, work.ID)
	}
}

// ADR-27: a match under a root folder the filter does not name must not surface,
// mirroring TestListWorks_RootFolderFilterNarrowsToLinkedWorks.
func TestSearch_RootFolderFilterNarrowsToLinkedWorks(t *testing.T) {
	opened, _ := newLibrary(t)
	inRoot1 := createWork(t, opened, "music", "amber rivers")
	inRoot2 := createWork(t, opened, "music", "amber springs")
	linkRootFolder(t, opened, inRoot1.ID, 1)
	linkRootFolder(t, opened, inRoot2.ID, 2)

	results, err := library.NewSearch(opened).Works(t.Context(), "amber", "", []int64{1}, nil, 10)
	if err != nil {
		t.Fatalf("Works: %v", err)
	}
	if found := searchOne(t, results); found.ID != inRoot1.ID {
		t.Errorf("matched work %d, want only the work linked to root folder 1 (%d)", found.ID, inRoot1.ID)
	}
}

// A nil rootFolderIDs must be unfiltered, but a non-nil empty slice -- "the caller can
// see no root folder at all" -- must match nothing, same distinction as
// TestListWorks_EmptyNonNilRootFolderFilterMatchesNothing. This is the security-critical
// case: mutation-tested by forcing filterRootFolders to always false in search.go and
// confirming this test then fails.
func TestSearch_EmptyNonNilRootFolderFilterMatchesNothing(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "amber rivers")
	linkRootFolder(t, opened, work.ID, 1)

	nilFiltered, err := library.NewSearch(opened).Works(t.Context(), "amber", "", nil, nil, 10)
	if err != nil {
		t.Fatalf("Works (nil): %v", err)
	}
	if len(nilFiltered) != 1 {
		t.Errorf("nil rootFolderIDs excluded a row, want unfiltered: got %+v", nilFiltered)
	}

	emptyFiltered, err := library.NewSearch(opened).Works(t.Context(), "amber", "", []int64{}, nil, 10)
	if err != nil {
		t.Fatalf("Works (empty): %v", err)
	}
	if len(emptyFiltered) != 0 {
		t.Errorf("empty non-nil rootFolderIDs = %+v, want nothing visible to mean nothing returned", emptyFiltered)
	}
}
