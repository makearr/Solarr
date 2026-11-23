package tv

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/mediaserver"
)

func TestASeriesOnlyTheMediaServerKnowsIsMarkedProvisional(t *testing.T) {
	server := FromServer(mediaserver.Item{Title: "Example Show", Year: 2020}, "local:jellyfin")
	provider := Series{Title: "Example Show", Overview: "Provider words.", Sources: []string{ProviderTMDB}}
	cases := []struct {
		name   string
		merged Series
		want   library.MetadataMark
	}{
		{"only the server answered", Merge(server), library.MarkProvisional},
		{"a provider answered too", Merge(provider, server), library.MarkSettled},
	}
	for _, c := range cases {
		if mark := ToWrite(c.merged, WriteOptions{}).Mark; mark != c.want {
			t.Errorf("%s: mark = %v, want %v", c.name, mark, c.want)
		}
	}
}
