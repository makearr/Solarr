package music

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxImageOptions bounds what one picker lists.
const maxImageOptions = 40

// CoverArtLister lists every picture the Cover Art Archive holds for a release or release group.
type CoverArtLister interface {
	List(ctx context.Context, entity, mbid string) ([]musicbrainz.CoverImage, error)
}

// ArtworkChoiceConfig is what an ArtworkChoice needs; the two image funcs are optional.
type ArtworkChoiceConfig struct {
	Artworks library.Artworks
	CoverArt CoverArtLister
	// AlbumImages lists a release group's pictures from a further provider.
	AlbumImages func(ctx context.Context, releaseGroupMBID string) ([]media.ImageOption, error)
	// ArtistImages lists an artist's pictures from every provider that has some.
	ArtistImages func(ctx context.Context, artistMBID string) ([]media.ImageOption, error)
	HTTP         *httpx.Client
	FS           fsx.FS
	ImagesDir    string
	Clock        clock.Clock
}

// ArtworkChoice lets the user browse the pictures providers hold for an album or artist and pick one;
// the pick replaces the recorded image and is kept, as enrichment never overwrites an existing one.
type ArtworkChoice struct {
	config ArtworkChoiceConfig
	store  *ArtworkJob
}

// NewArtworkChoice builds it.
func NewArtworkChoice(config ArtworkChoiceConfig) *ArtworkChoice {
	return &ArtworkChoice{config: config, store: NewArtworkJob(ArtworkJobConfig{
		Artworks: config.Artworks, HTTP: config.HTTP, FS: config.FS, ImagesDir: config.ImagesDir, Clock: config.Clock,
	})}
}

// WorkPictures implements media.Picturing: the archive's pictures of the release group and its editions, then the
// further provider's.
func (c *ArtworkChoice) WorkPictures(ctx context.Context, workID int64) ([]media.ImageOption, error) {
	ids, err := c.config.Artworks.MBIDsForWork(ctx, workID)
	if err != nil {
		return nil, err
	}
	var options []media.ImageOption
	var failures []error
	collect := func(entity, mbid string) {
		images, err := c.config.CoverArt.List(ctx, entity, mbid)
		if err != nil {
			failures = append(failures, err)
			return
		}
		for _, image := range images {
			options = append(options, media.ImageOption{
				URL: image.URL, ThumbURL: image.ThumbURL, Source: "Cover Art Archive", Label: joinLabels(image.Types),
			})
		}
	}
	if ids.ReleaseGroup != "" {
		collect("release-group", ids.ReleaseGroup)
	}
	for _, release := range ids.Releases {
		collect("release", release)
	}
	if ids.ReleaseGroup != "" && c.config.AlbumImages != nil {
		more, err := c.config.AlbumImages(ctx, ids.ReleaseGroup)
		if err != nil {
			failures = append(failures, err)
		}
		options = append(options, more...)
	}
	return distinctOptions(options), optionsError(options, failures)
}

// PersonOptions lists the pictures on offer for an artist.
func (c *ArtworkChoice) PersonOptions(ctx context.Context, personID int64) ([]media.ImageOption, error) {
	mbid, err := c.config.Artworks.ArtistMBID(ctx, personID)
	if err != nil || mbid == "" || c.config.ArtistImages == nil {
		return nil, err
	}
	options, err := c.config.ArtistImages(ctx, mbid)
	return distinctOptions(options), optionsError(options, []error{err})
}

// PickWorkPicture implements media.Picturing: imageURL, one of WorkPictures, becomes the work's cover.
func (c *ArtworkChoice) PickWorkPicture(ctx context.Context, workID int64, imageURL string) error {
	options, err := c.WorkPictures(ctx, workID)
	if err != nil && len(options) == 0 {
		return err
	}
	if !offered(options, imageURL) {
		return media.ErrImageNotOffered
	}
	source, err := c.store.download(ctx, imageURL)
	if err != nil {
		return err
	}
	return c.store.storeCover(ctx, workID, imageURL, source)
}

// PickPerson makes imageURL, one of PersonOptions, the artist's picture.
func (c *ArtworkChoice) PickPerson(ctx context.Context, personID int64, imageURL string) error {
	options, err := c.PersonOptions(ctx, personID)
	if err != nil && len(options) == 0 {
		return err
	}
	if !offered(options, imageURL) {
		return media.ErrImageNotOffered
	}
	source, err := c.store.download(ctx, imageURL)
	if err != nil {
		return err
	}
	return c.store.storeImage(ctx, personID, imageURL, source)
}

// optionsError is the failures worth reporting: none when something was found, so one provider being
// down does not hide what another listed.
func optionsError(options []media.ImageOption, failures []error) error {
	if len(options) > 0 {
		return nil
	}
	return errors.Join(failures...)
}

func distinctOptions(options []media.ImageOption) []media.ImageOption {
	var distinct []media.ImageOption
	seen := map[string]bool{}
	for _, option := range options {
		if option.URL == "" || seen[option.URL] {
			continue
		}
		seen[option.URL] = true
		if option.ThumbURL == "" {
			option.ThumbURL = option.URL
		}
		distinct = append(distinct, option)
	}
	if len(distinct) > maxImageOptions {
		distinct = distinct[:maxImageOptions]
	}
	return distinct
}

func offered(options []media.ImageOption, imageURL string) bool {
	return slices.ContainsFunc(options, func(option media.ImageOption) bool { return option.URL == imageURL })
}

func joinLabels(labels []string) string {
	if len(labels) == 0 {
		return "Image"
	}
	return strings.Join(labels, ", ")
}
