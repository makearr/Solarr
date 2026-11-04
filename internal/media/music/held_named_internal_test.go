package music

import (
	"context"
	"log/slog"
	"testing"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

type namedAlbumLookup struct{}

func (namedAlbumLookup) Search(context.Context, musicbrainz.Priority, string, string, int) ([]musicbrainz.ReleaseGroupMatch, error) {
	return nil, nil
}

func (namedAlbumLookup) SearchRecordings(context.Context, musicbrainz.Priority, string, string, int) ([]musicbrainz.RecordingMatch, error) {
	return nil, nil
}

func (namedAlbumLookup) Album(_ context.Context, _ musicbrainz.Priority, rg, _ string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	return musicbrainz.ReleaseGroup{MBID: rg, Title: "Wind of Change", ArtistCredit: []musicbrainz.ArtistCredit{{Name: "Scorpions"}}}, musicbrainz.Release{}, nil
}

func (namedAlbumLookup) Releases(context.Context, musicbrainz.Priority, string) ([]musicbrainz.Release, error) {
	return nil, nil
}

func TestNamedAlbumPlacement_UntaggedFilesTakeTheNamedAlbumAndStayApart(t *testing.T) {
	importer := Importer{config: ImporterConfig{Albums: namedAlbumLookup{}}, logger: slog.Default()}
	unit := inspect.Unit{Media: []inspect.File{{RelPath: "track1.flac"}, {RelPath: "track2.flac"}}}
	request := batch.HeldRequest{Unit: unit, As: &batch.Named{Provider: ProviderReleaseGroup, ExternalID: "rg-wind"}}

	placement := importer.namedAlbumPlacement(t.Context(), request, []mediatool.Probe{{}, {}})
	if placement == nil || placement.AlbumTitle != "Wind of Change" || placement.ArtistName != "Scorpions" {
		t.Fatalf("placement = %+v, want the named album's title and artist", placement)
	}
	first, _ := placement.trackOf("track1.flac", mediatool.Tags{})
	second, _ := placement.trackOf("track2.flac", mediatool.Tags{})
	if first.Title != "track1" || second.Title != "track2" {
		t.Errorf("titles = %q, %q, want the file names so untagged files do not collide", first.Title, second.Title)
	}

	if guess := (batch.HeldRequest{Unit: unit, As: request.As, Guess: 0.5}); importer.namedAlbumPlacement(t.Context(), guess, nil) != nil {
		t.Error("a guess is not a person naming the album")
	}
}
