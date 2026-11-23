package adult

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/mediaserver"
)

func TestASceneOnlyTheMediaServerKnowsIsMarkedProvisional(t *testing.T) {
	server := FromServer(mediaserver.Item{Title: "Invented Scene", Year: 2021}, "local:stash")
	provider := Scene{Title: "Invented Scene", Overview: "Provider words.", Sources: []string{sourceWhisparr}}
	cases := []struct {
		name   string
		merged Scene
		want   library.MetadataMark
	}{
		{"only the server answered", server, library.MarkProvisional},
		{"a provider answered too", Merge(provider, server), library.MarkSettled},
	}
	for _, c := range cases {
		if mark := ToWrite(c.merged, time.Time{}).Mark; mark != c.want {
			t.Errorf("%s: mark = %v, want %v", c.name, mark, c.want)
		}
	}
}
