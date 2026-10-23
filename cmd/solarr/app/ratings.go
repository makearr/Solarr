package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
)

// readCertificationAges reads the age of each certification stored before ages were, with its media type's reading;
// a failure is logged, never fatal: those works count as unrated until their next refresh.
func (p *process) readCertificationAges(ctx context.Context) {
	read, err := library.NewPeople(p.library).BackfillCertificationAges(ctx, func(mediaType, rating string) int64 {
		if reader, ok := media.Capability[media.Rating](p.media, media.Key(mediaType)); ok {
			if age, known := reader.RatingAge(rating); known {
				return age
			}
		}
		return -1
	})
	if err != nil {
		p.logger.Warn("certification ages not read", slog.Any("error", err))
	}
	if read > 0 {
		p.logger.Info("certification ages read", slog.Int("certifications", read))
	}
}

// ratingAdmits says whether a library's maximum rating lets in a work admitting age (-1 unrated).
func (p *process) ratingAdmits(categoryID, age int64) bool {
	category, found := p.cache.Snapshot().Category(categoryID)
	return !found || category.AdmitsAge(age)
}

// ratingLimit is a library's maximum rating; false when it has none.
func (p *process) ratingLimit(categoryID int64) (library.RatingLimit, bool) {
	category, found := p.cache.Snapshot().Category(categoryID)
	if !found || category.MaxAge == nil {
		return library.RatingLimit{}, false
	}
	return library.RatingLimit{MaxAge: *category.MaxAge, AllowUnrated: category.AllowUnrated}, true
}
