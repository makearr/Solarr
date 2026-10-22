// Package picturesweep keeps the posters, organisation logos and first-billed photos a library of one media type shows
// in the picture cache, so a picture fetched when it is first seen does not cost the viewer the CDN's latency.
package picturesweep

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

const (
	// perRun bounds one run so a first pass over a big library is spread over several.
	perRun = 1500
	// workers fetch side by side: a provider's CDN answers in about 0.7 s, so one at a time is seven posters a second at best.
	workers = 6
	// maxPhotos bounds the people whose photo is kept ready: the billed-first of each work.
	maxPhotos = 6000
)

// Reader lists the picture addresses of a media type's works, organisations and billed-first people.
type Reader interface {
	PictureAddressesOf(ctx context.Context, mediaType, organisationKind string, maxPhotos int) (library.PictureSet, error)
}

// Cache is the provider picture cache as the sweep needs it.
type Cache interface {
	Cached(address string) bool
	Ensure(ctx context.Context, address string) (string, error)
}

// Config is what a Job needs; OrganisationKind is the organisations whose logos are shown (networks, studios).
type Config struct {
	MediaType        string
	OrganisationKind string
	Reader           Reader
	Cache            Cache
	Logger           *slog.Logger
}

// Job fetches what the cache lacks, posters first.
type Job struct {
	config Config
	logger *slog.Logger
}

// New builds the handler; Logger is optional.
func New(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{config: config, logger: logger}
}

// Run fetches what is missing, posters first, at most perRun pictures.
func (j *Job) Run(ctx context.Context, _ jobs.Job) error {
	set, err := j.config.Reader.PictureAddressesOf(ctx, j.config.MediaType, j.config.OrganisationKind, maxPhotos)
	if err != nil {
		return err
	}
	var missing []string
	for _, group := range [][]string{set.Posters, set.Logos, set.Photos} {
		for _, address := range group {
			if !j.config.Cache.Cached(address) {
				missing = append(missing, address)
			}
		}
	}
	total := len(missing)
	if len(missing) > perRun {
		missing = missing[:perRun]
	}
	fetched, bad := 0, 0
	outcomes := parallel.Do(ctx, len(missing), workers, func(ctx context.Context, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := j.config.Cache.Ensure(ctx, missing[index])
		return err
	})
	for _, err := range outcomes {
		switch {
		case err == nil:
			fetched++
		case ctx.Err() == nil:
			bad++
		}
	}
	j.logger.Info("pictures fetched", slog.String("media_type", j.config.MediaType),
		slog.Int("fetched", fetched), slog.Int("failed", bad), slog.Int("still_missing", total-fetched-bad))
	return ctx.Err()
}
