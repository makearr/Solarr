package book

import (
	"slices"
	"testing"
)

func TestSubjectGenresTryEachSubjectAndItsPartBeforeAComma(t *testing.T) {
	got := subjectGenres([]string{"Fantasy fiction, English", "Rats", "Fantasy fiction"})
	if want := []string{"Fantasy fiction, English", "Fantasy fiction", "Rats"}; !slices.Equal(got, want) {
		t.Errorf("subjectGenres = %v, want %v", got, want)
	}
}
