package book

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/mediaserver"
)

func TestABookOnlyTheMediaServerKnowsIsMarkedProvisional(t *testing.T) {
	server := FromServer(mediaserver.Item{Title: "The Lantern Keeper", Year: 2015}, "local:audiobookshelf")
	provider := Book{Title: "The Lantern Keeper", Overview: "Provider words.", Sources: []string{sourceOpenLibrary}}
	cases := []struct {
		name   string
		merged Book
		want   library.MetadataMark
	}{
		{"only the server answered", server, library.MarkProvisional},
		{"a provider answered too", Merge(provider, server), library.MarkSettled},
	}
	for _, c := range cases {
		if mark := ToWrite(Ebook, c.merged, time.Time{}).Mark; mark != c.want {
			t.Errorf("%s: mark = %v, want %v", c.name, mark, c.want)
		}
	}
}
