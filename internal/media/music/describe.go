package music

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

var _ media.Describing = Adder{}

// DescribeWork implements media.Describing: an album's credits, genres, rating and tracklist from MusicBrainz, nothing
// stored. An artist has no preview beyond its lookup entry.
func (a Adder) DescribeWork(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	if request.Kind == AddKindArtist {
		return media.WorkPreview{}, media.ErrNotWired
	}
	if err := checkMBID(request.ExternalID); err != nil {
		return media.WorkPreview{}, err
	}
	ctx = musicbrainz.ForLibrary(ctx, request.CategoryID)
	group, release, err := a.Provider.Album(ctx, musicbrainz.PriorityInteractive, request.ExternalID, "")
	if errors.Is(err, musicbrainz.ErrNotFound) {
		return media.WorkPreview{}, fmt.Errorf("%w: release group %s", media.ErrUnknownCandidate, request.ExternalID)
	}
	if err != nil {
		return media.WorkPreview{}, fmt.Errorf("musicbrainz: %w", err)
	}
	return PreviewOf(group, release), nil
}

// PreviewOf is a release group and one of its releases as a preview; the tracks are grouped by disc.
func PreviewOf(group musicbrainz.ReleaseGroup, release musicbrainz.Release) media.WorkPreview {
	preview := media.WorkPreview{Title: group.Title, Year: partialDateYear(group.FirstReleaseDate), Status: group.PrimaryType}
	for i, credit := range group.ArtistCredit {
		if i == 0 {
			preview.Subtitle = credit.Name
		}
		preview.People = append(preview.People, media.PreviewPerson{Name: credit.ArtistName, Role: "artist"})
	}
	for _, genre := range group.Genres {
		preview.Genres = append(preview.Genres, genre.Name)
	}
	if group.Rating.Votes > 0 {
		preview.Ratings = []media.PreviewRating{{Provider: "musicbrainz", Score: group.Rating.Value * 2, Votes: int64(group.Rating.Votes)}}
	}
	addFact := func(key, value string) {
		if value != "" {
			preview.Facts = append(preview.Facts, media.PreviewFact{Key: key, Value: value})
		}
	}
	addFact(media.FactPublisher, release.LabelName)
	addFact(media.FactReleased, release.Date)
	for _, medium := range release.Media {
		disc := media.PreviewGroup{Number: medium.Position, Title: medium.Title}
		for _, track := range medium.Tracks {
			if track.Optional {
				continue
			}
			disc.Parts = append(disc.Parts, media.PreviewPart{Number: strconv.Itoa(track.Position), Title: track.Title, DurationSec: int(track.DurationMs / 1000)})
		}
		preview.Groups = append(preview.Groups, disc)
	}
	return preview
}
