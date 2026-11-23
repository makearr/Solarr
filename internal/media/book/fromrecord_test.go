package book

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

type fakeRecords struct{ asked []string }

func (*fakeRecords) Namespace() string { return "own" }
func (f *fakeRecords) Search(_ context.Context, query specprovider.Query) ([]specprovider.Record, error) {
	f.asked = append(f.asked, query.Title)
	return []specprovider.Record{{
		IDs: map[string]string{"own": "77", "isbn13": "9780000000002"}, Title: "The Lantern Keeper", Year: 2019, Overview: "A keeper of lanterns.",
		Language: "en", PageCount: 320, Genres: []string{"Fantasy"}, Date: "2019-05-01T00:00:00Z",
		People:        []specprovider.Person{{Name: "Mara Quell", Role: "author"}, {Name: "Ines Vale", Role: "narrator"}},
		Organisations: []specprovider.Organisation{{Name: "Lantern House", Role: "publisher"}},
		Images:        []specprovider.Image{{Kind: "cover", URL: "https://img.example.org/c.jpg"}},
		Rating:        specprovider.Rating{Value: 7.5, Votes: 12},
	}}, nil
}
func (f *fakeRecords) Item(context.Context, string) (specprovider.Record, error) {
	return specprovider.Record{}, context.Canceled
}

func TestFromRecordFillsTheBookAndCreditsByRole(t *testing.T) {
	provider := &fakeRecords{}
	records, _ := provider.Search(t.Context(), specprovider.Query{Title: "x"})
	got := FromRecord("custom_books", records[0])
	if got.Title != "The Lantern Keeper" || got.Year != 2019 || got.Pages != 320 || got.Publisher != "Lantern House" || got.Published != "2019-05-01" ||
		got.IDs.ISBN != "9780000000002" || got.Language != "en" || len(got.Genres) == 0 || len(got.Images) != 1 || got.Images[0].Source != "custom_books" {
		t.Fatalf("book = %+v", got)
	}
	if len(got.Credits) != 2 || got.Credits[0].Role != RoleAuthor || got.Credits[1].Role != RoleNarrator || got.Ratings[0].Provider != "custom_books" {
		t.Errorf("credits %+v ratings %+v", got.Credits, got.Ratings)
	}
}

func TestFetchForTakesADefinitionLastAndOnlyFillsWhatTheCompiledProviderLacks(t *testing.T) {
	records := &fakeRecords{}
	service := Service{
		Format: Ebook, OpenLibrary: &fakeOpenLibrary{}, Records: func(key string) RecordProvider {
			if key == "custom_books" {
				return records
			}
			return nil
		},
		PriorityFor: func(int64) []string { return []string{"openlibrary", "custom_books", "gone_definition"} },
	}
	got, err := service.FetchFor(t.Context(), 7, IDs{OpenLibrary: "OL1001W"}, Hint{Title: "The Lantern Keeper"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records.asked) != 1 || records.asked[0] != "The Lantern Keeper" {
		t.Fatalf("the definition was asked %v, want once by the title", records.asked)
	}
	if got.Year != 1987 || got.Overview != "Open Library's own words." {
		t.Errorf("the definition overwrote what the compiled provider held: year %d overview %q", got.Year, got.Overview)
	}
	if got.Credits[0].Name != "Mara Quell" {
		t.Errorf("credits = %+v", got.Credits)
	}
}

func TestSearchOneOfADefinitionAndOfAGoneOne(t *testing.T) {
	service := Service{Format: Ebook, Records: func(key string) RecordProvider {
		if key == "custom_books" {
			return &fakeRecords{}
		}
		return nil
	}}
	found, err := service.searchOne(t.Context(), "custom_books", "Lantern", "", 5)
	if err != nil || len(found) != 1 || found[0].Author != "Mara Quell" || found[0].Cover == "" {
		t.Fatalf("found %+v, %v", found, err)
	}
	if none, err := service.searchOne(t.Context(), "gone_definition", "Lantern", "", 5); err != nil || len(none) != 0 {
		t.Errorf("a gone definition answered %+v, %v", none, err)
	}
}
