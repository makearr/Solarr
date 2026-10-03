package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func creditedSeries() library.SeriesWrite {
	write := sampleSeries()
	write.Keywords = []string{"heist"}
	write.MoreNetworks = []library.SeriesNetworkWrite{{Name: "Other Net", Country: "US", Provider: "tmdb", ExternalID: "9", LogoURL: "https://example.test/n.png"}}
	write.Credits = []library.SeriesCreditWrite{
		{Provider: "tmdb", ExternalID: "11", Name: "A Person", ImageURL: "https://example.test/a.jpg", Role: "actor", Character: "A Role", Ord: 0},
		{Provider: "tmdb", ExternalID: "12", Name: "B Person", Role: "creator"},
		{Provider: "tmdb", ExternalID: "21", Name: "G Star", Role: "guest_star", Character: "A Guest", Ord: 500, PartRef: "s01e01"},
		{Provider: "tmdb", ExternalID: "22", Name: "Lost Episode", Role: "director", PartRef: "s09e09"},
	}
	return write
}

func TestSeriesCreditsAreStoredOnTheWorkAndOnTheirEpisode(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	result, err := series.Upsert(t.Context(), seriesAt, creditedSeries())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := series.Upsert(t.Context(), seriesAt, creditedSeries()); err != nil {
		t.Fatal(err)
	}

	var credits, scoped, people int
	if err := opened.Read().QueryRowContext(t.Context(),
		`SELECT COUNT(*), COALESCE(SUM(part_id != 0), 0) FROM credit WHERE work_id = ?`, result.WorkID).Scan(&credits, &scoped); err != nil {
		t.Fatal(err)
	}
	if credits != 3 || scoped != 1 {
		t.Errorf("credits = %d, scoped = %d; want 3 (the unknown episode's skipped, a refresh adds nothing twice) and 1", credits, scoped)
	}
	var partOf int64
	if err := opened.Read().QueryRowContext(t.Context(), `SELECT part_id FROM credit WHERE role = 'guest_star'`).Scan(&partOf); err != nil {
		t.Fatal(err)
	}
	if partOf != result.PartIDs["s01e01"] {
		t.Errorf("guest star on part %d, want %d", partOf, result.PartIDs["s01e01"])
	}
	if err := opened.Read().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM person`).Scan(&people); err != nil || people != 3 {
		t.Errorf("people = %d, err = %v; want 3", people, err)
	}
	var image string
	if err := opened.Read().QueryRowContext(t.Context(),
		`SELECT json_extract(p.fields_json, '$.image_url') FROM person p JOIN person_external_id e ON e.person_id = p.id WHERE e.external_id = '11'`).Scan(&image); err != nil || image != "https://example.test/a.jpg" {
		t.Errorf("image = %q, err = %v", image, err)
	}
}

func TestSeriesNetworksKeywordsAndLogoAreStored(t *testing.T) {
	opened, _ := newLibrary(t)
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, creditedSeries())
	if err != nil {
		t.Fatal(err)
	}
	var networks int
	var logo string
	if err := opened.Read().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM work_organisation WHERE work_id = ? AND role = 'network'`, result.WorkID).Scan(&networks); err != nil || networks != 2 {
		t.Errorf("networks = %d, err = %v; want the primary and the other", networks, err)
	}
	if err := opened.Read().QueryRowContext(t.Context(),
		`SELECT json_extract(fields_json, '$.logo') FROM organisation WHERE name = 'Other Net'`).Scan(&logo); err != nil || logo != "https://example.test/n.png" {
		t.Errorf("logo = %q, err = %v", logo, err)
	}
	var keyword string
	if err := opened.Read().QueryRowContext(t.Context(),
		`SELECT value FROM work_classification WHERE work_id = ? AND kind = 'keyword'`, result.WorkID).Scan(&keyword); err != nil || keyword != "heist" {
		t.Errorf("keyword = %q, err = %v", keyword, err)
	}
}
