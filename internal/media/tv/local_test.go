package tv

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/metadata/anidb"
	"github.com/makearr/Solarr/internal/metadata/myanimelist"
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

func TestAnimeScoresBecomeASeriesRating(t *testing.T) {
	mal := FromMAL(myanimelist.Anime{Title: "Harbour Lights", Score: 8.12, Votes: 4000, Poster: "p"})
	if mal.Rating != 8.12 || mal.RatingVotes != 4000 || mal.Images[0].URL != "p" || mal.Sources[0] != "myanimelist" {
		t.Errorf("mal = %+v", mal)
	}
	db := FromAniDB(anidb.Anime{Title: "Minato", Description: "d", Score: 7.5, Votes: 12})
	if db.Rating != 7.5 || db.RatingVotes != 12 || db.Overview != "d" || db.Sources[0] != "anidb" {
		t.Errorf("anidb = %+v", db)
	}
}
