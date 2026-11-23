package tv

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
)

func TestServerCreditsAreKeyedByName(t *testing.T) {
	series := FromServer(mediaserver.Item{Title: "Harbour Lights", People: []mediaserver.Credit{
		{Name: "Ada  Quill", Role: "Captain", Kind: "actor"},
		{Name: "Bo Rend", Kind: "director"},
		{Name: "Cy Fenn", Kind: "photographer"},
	}}, "local:jellyfin")
	want := []Credit{
		{Name: "Ada  Quill", Role: RoleActor, Character: "Captain"},
		{Name: "Bo Rend", Role: RoleDirector},
	}
	if !reflect.DeepEqual(series.Credits, want) {
		t.Fatalf("credits = %+v, want %+v", series.Credits, want)
	}
	write := namedCreditWrite(series.Credits[0])
	if write.Provider != mediaserver.PersonProvider || write.ExternalID != "ada quill" || write.Name != "Ada  Quill" {
		t.Fatalf("write = %+v", write)
	}
}
