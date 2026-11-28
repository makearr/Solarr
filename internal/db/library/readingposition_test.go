package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestReadingPositions_KeepOnePlacePerUserAndFile(t *testing.T) {
	opened, _ := newLibrary(t)
	if _, err := opened.WriteDB().ExecContext(t.Context(), `INSERT INTO media_object (id, size_bytes, quick_hash, first_seen_at) VALUES (5, 1, 'a', 1)`); err != nil {
		t.Fatal(err)
	}
	places := library.NewReadingPositions(opened)
	if _, found, err := places.Get(t.Context(), 1, 5); found || err != nil {
		t.Fatalf("a file never read = found %v, %v; want not found", found, err)
	}
	for _, place := range []struct {
		user     int64
		position string
		progress float64
	}{{1, "3", 0.1}, {1, "7", 0.4}, {2, "epubcfi(/6/2)", 0.9}} {
		if err := places.Set(t.Context(), place.user, 5, library.ReadingPosition{Position: place.position, Progress: place.progress, UpdatedAt: 9}); err != nil {
			t.Fatal(err)
		}
	}
	first, _, _ := places.Get(t.Context(), 1, 5)
	second, _, _ := places.Get(t.Context(), 2, 5)
	if first != (library.ReadingPosition{Position: "7", Progress: 0.4, UpdatedAt: 9}) || second.Position != "epubcfi(/6/2)" {
		t.Errorf("places = %+v and %+v, want user 1's last place (7) and user 2's own", first, second)
	}
}
