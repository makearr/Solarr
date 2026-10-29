package book

import "testing"

func TestAudiobookLevelsOfferNarratorsOnTheirRoleAndABookDoesNot(t *testing.T) {
	var audiobook []string
	for _, level := range Audiobook.Levels() {
		audiobook = append(audiobook, level.Key+":"+level.Role)
	}
	want := []string{"books:", "authors:", "narrators:narrator", "series:", "publishers:"}
	if len(audiobook) != len(want) {
		t.Fatalf("audiobook levels = %v, want %v", audiobook, want)
	}
	for index := range want {
		if audiobook[index] != want[index] {
			t.Fatalf("audiobook levels = %v, want %v", audiobook, want)
		}
	}
	for _, level := range Ebook.Levels() {
		if level.Key == LevelNarrators {
			t.Errorf("an ebook library has a Narrators level")
		}
	}
}
