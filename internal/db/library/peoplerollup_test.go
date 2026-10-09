package library_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
)

// rollupRows reads a library rollup as sorted "subject category role works latest" lines.
func rollupRows(t *testing.T, opened *store.Store, query string) []string {
	t.Helper()
	rows, err := opened.Read().QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var subject, category, works int64
		var role string
		var latest *int64
		if err := rows.Scan(&subject, &category, &role, &works, &latest); err != nil {
			t.Fatal(err)
		}
		year := int64(0)
		if latest != nil {
			year = *latest
		}
		out = append(out, fmt.Sprintf("%d %d %q %d %d", subject, category, role, works, year))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

const recountPeople = `SELECT c.person_id, d.category_id, c.role, COUNT(DISTINCT c.work_id), MAX(w.year)
FROM credit c JOIN work w ON w.id = c.work_id JOIN desired_item d ON d.work_id = c.work_id
GROUP BY c.person_id, d.category_id, c.role
UNION ALL
SELECT c.person_id, d.category_id, '', COUNT(DISTINCT c.work_id), MAX(w.year)
FROM credit c JOIN work w ON w.id = c.work_id JOIN desired_item d ON d.work_id = c.work_id
GROUP BY c.person_id, d.category_id`

const recountOrganisations = `SELECT o.organisation_id, d.category_id, o.role, COUNT(DISTINCT o.work_id), MAX(w.year)
FROM work_organisation o JOIN work w ON w.id = o.work_id JOIN desired_item d ON d.work_id = o.work_id
GROUP BY o.organisation_id, d.category_id, o.role
UNION ALL
SELECT o.organisation_id, d.category_id, '', COUNT(DISTINCT o.work_id), MAX(w.year)
FROM work_organisation o JOIN work w ON w.id = o.work_id JOIN desired_item d ON d.work_id = o.work_id
GROUP BY o.organisation_id, d.category_id`

// assertRollupsCurrent checks both rollups against a count from scratch, the one thing they must always equal.
func assertRollupsCurrent(t *testing.T, opened *store.Store, step string) {
	t.Helper()
	stored := rollupRows(t, opened, `SELECT person_id, category_id, role, works, latest FROM person_library`)
	if want := rollupRows(t, opened, recountPeople); !slices.Equal(stored, want) {
		t.Errorf("%s: person_library =\n%v\nwant\n%v", step, stored, want)
	}
	stored = rollupRows(t, opened, `SELECT organisation_id, category_id, role, works, latest FROM organisation_library`)
	if want := rollupRows(t, opened, recountOrganisations); !slices.Equal(stored, want) {
		t.Errorf("%s: organisation_library =\n%v\nwant\n%v", step, stored, want)
	}
}

func TestPeopleAndOrganisationsFollowTheirSeriesIntoAndOutOfALibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	result, err := series.Upsert(t.Context(), seriesAt, creditedSeries())
	if err != nil {
		t.Fatal(err)
	}
	assertRollupsCurrent(t, opened, "credited, in no library")
	if count := rollupRows(t, opened, `SELECT person_id, category_id, role, works, latest FROM person_library`); len(count) != 0 {
		t.Errorf("a series no library wants puts nobody in a library: %v", count)
	}

	desired := library.NewDesired(opened)
	for _, season := range []string{"s01", "s02"} {
		if _, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: season, CategoryID: 4, ProfileID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	assertRollupsCurrent(t, opened, "wanted in library 4")
	var cast int
	if err := opened.Read().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM person_library WHERE category_id = 4 AND role = ''`).Scan(&cast); err != nil || cast != 3 {
		t.Errorf("people in library 4 = %d, err = %v; want 3", cast, err)
	}

	refreshed := creditedSeries()
	refreshed.Credits = append(refreshed.Credits, library.SeriesCreditWrite{Provider: "tmdb", ExternalID: "13", Name: "New Cast", Role: "actor"})
	if _, err := series.Upsert(t.Context(), seriesAt, refreshed); err != nil {
		t.Fatal(err)
	}
	assertRollupsCurrent(t, opened, "refreshed with a new cast member")

	if err := removeWithFiles(t, opened, result.WorkID); err != nil {
		t.Fatal(err)
	}
	assertRollupsCurrent(t, opened, "deleted")
	if count := rollupRows(t, opened, `SELECT person_id, category_id, role, works, latest FROM person_library`); len(count) != 0 {
		t.Errorf("a deleted series leaves nobody in its library: %v", count)
	}
}

func TestAnArtistIsInTheLibraryTheirAlbumIsWantedIn(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	first := newWorkForCredits(t, identity, queries, "rg-1")
	second := newWorkForCredits(t, identity, queries, "rg-2")
	artist, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "a-1", Person: library.PersonIdentity{Name: "Vutu Doci", Kind: "Person"},
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "local", ExternalID: "vutu doci", Person: library.PersonIdentity{Name: "Vutu Doci", Kind: "Person"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, credit := range []struct{ work, person int64 }{{first, artist}, {second, other}} {
		if err := people.AddLeadCredit(t.Context(), credit.work, credit.person, "artist"); err != nil {
			t.Fatal(err)
		}
	}
	desired := library.NewDesired(opened)
	for _, work := range []int64{first, second} {
		if _, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{WorkID: work, CategoryID: 2, ProfileID: 1, Monitored: true}); err != nil {
			t.Fatal(err)
		}
	}
	assertRollupsCurrent(t, opened, "two albums wanted")

	if err := people.MergeArtists(t.Context(), []library.ArtistMerge{{WinnerID: artist, LoserID: other, LoserName: "Vutu Doci"}}); err != nil {
		t.Fatal(err)
	}
	assertRollupsCurrent(t, opened, "merged")
	var works int
	if err := opened.Read().QueryRowContext(t.Context(),
		`SELECT works FROM person_library WHERE person_id = ? AND category_id = 2 AND role = 'artist'`, artist).Scan(&works); err != nil || works != 2 {
		t.Errorf("merged artist's albums = %d, err = %v; want 2", works, err)
	}

	if _, err := identity.RemoveWork(t.Context(), first, false); err != nil {
		t.Fatal(err)
	}
	assertRollupsCurrent(t, opened, "one album removed")
}
