package music_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// listedCoverArt answers canned pictures per "entity/mbid".
type listedCoverArt map[string][]musicbrainz.CoverImage

func (l listedCoverArt) List(_ context.Context, entity, mbid string) ([]musicbrainz.CoverImage, error) {
	return l[entity+"/"+mbid], nil
}

func TestArtworkChoice_ListsDistinctOptionsAndKeepsAPick(t *testing.T) {
	server := testImageServer(t)
	_, opened, memFS := newArtworkJob(t, &fakeCoverArt{}, server.URL)
	workID := refineWithReleaseMBIDForArtworkJob(t, library.NewIdentity(opened), "rg-1", "release-1")

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fake := clock.NewFakeAtEpoch()
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, []string{parsed.Hostname()}),
		httpx.NewLimiter(fake, httpx.Config{}, func(time.Duration) time.Duration { return 0 }),
		httpx.NewBreaker(fake, httpx.BreakerConfig{}), httpx.ClientConfig{Provider: "test-image-server", UserAgent: "test"})
	cover := server.URL + "/cover.png"
	choice := music.NewArtworkChoice(music.ArtworkChoiceConfig{
		Artworks: library.NewArtworks(opened),
		CoverArt: listedCoverArt{
			"release-group/rg-1": {{URL: cover, ThumbURL: cover, Types: []string{"Front"}}},
			"release/release-1":  {{URL: cover, Types: []string{"Front"}}, {URL: server.URL + "/back.png", Types: []string{"Back", "Booklet"}}},
		},
		HTTP: client, FS: memFS, ImagesDir: "/cache/images", Clock: fake,
	})

	options, err := choice.WorkPictures(t.Context(), workID)
	if err != nil {
		t.Fatalf("WorkPictures: %v", err)
	}
	if len(options) != 2 || options[0].URL != cover || options[1].Label != "Back, Booklet" || options[1].ThumbURL != options[1].URL {
		t.Fatalf("options = %+v, want the shared front once and the back with both labels", options)
	}
	if err := choice.PickWorkPicture(t.Context(), workID, "https://elsewhere.invalid/x.png"); !errors.Is(err, media.ErrImageNotOffered) {
		t.Fatalf("PickWorkPicture of an unlisted address = %v, want ErrImageNotOffered", err)
	}
	if err := choice.PickWorkPicture(t.Context(), workID, cover); err != nil {
		t.Fatalf("PickWorkPicture: %v", err)
	}
	recorded, err := library.NewArtworks(opened).GetForWork(t.Context(), workID)
	if err != nil || !recorded.Found {
		t.Fatalf("GetForWork = %+v, %v; want the picked cover recorded", recorded, err)
	}
}
