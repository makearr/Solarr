package tv

import "context"

// AddAltTitles lets the external tests drive the alternative-title pass directly.
func (s Service) AddAltTitles(ctx context.Context, series *Series, categoryID int64, recentOnly bool) {
	s.addAltTitles(ctx, series, categoryID, recentOnly)
}
