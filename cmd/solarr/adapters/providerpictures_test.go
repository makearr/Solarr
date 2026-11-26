package adapters_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Any media type's stored provider pictures are offered, and a pick moves to the front as the person's own.
func TestProviderPictures_OffersAndPicksStoredImages(t *testing.T) {
	opened := newSuggestLibrary(t)
	result, err := library.NewSeries(opened).Upsert(t.Context(), clock.NewFakeAtEpoch().Now(), library.SeriesWrite{
		Work: library.WorkIdentity{MediaType: "book", CanonicalTitle: "Invented Tale", SortTitle: "invented tale"},
		IDs:  []library.ExternalIDWrite{{Provider: "openlibrary", ExternalID: "OL1W", Confidence: 1}},
		Fields: []library.SeriesFieldWrite{{Field: "images", Provider: "openlibrary",
			Value: `[{"kind":"poster","url":"https://x.test/a.jpg","source":"openlibrary"},{"kind":"poster","url":"https://x.test/b.jpg","source":"googlebooks"},{"kind":"fanart","url":"https://x.test/c.jpg"}]`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pictures := adapters.ProviderPictures{Reader: library.NewSeriesReader(opened), Series: library.NewSeries(opened), Clock: clock.NewFakeAtEpoch()}
	got, err := pictures.WorkPictures(t.Context(), result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	want := []media.ImageOption{
		{URL: "https://x.test/a.jpg", ThumbURL: "https://x.test/a.jpg", Source: "openlibrary", Label: "Poster"},
		{URL: "https://x.test/b.jpg", ThumbURL: "https://x.test/b.jpg", Source: "googlebooks", Label: "Poster"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pictures = %+v, want %+v", got, want)
	}
	if err := pictures.PickWorkPicture(t.Context(), result.WorkID, "https://x.test/b.jpg"); err != nil {
		t.Fatal(err)
	}
	got, _ = pictures.WorkPictures(t.Context(), result.WorkID)
	if got[0].URL != "https://x.test/b.jpg" || len(got) != 2 {
		t.Fatalf("after pick = %+v", got)
	}
	if err := pictures.PickWorkPicture(t.Context(), result.WorkID, "https://x.test/zzz.jpg"); !errors.Is(err, media.ErrImageNotOffered) {
		t.Fatalf("pick of an unlisted address: %v", err)
	}
}
