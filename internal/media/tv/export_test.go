package tv

import "context"

// AddAltTitles lets the external tests drive the alternative-title pass directly.
func (s Service) AddAltTitles(ctx context.Context, series *Series, categoryID int64, recentOnly bool) {
	s.addAltTitles(ctx, series, categoryID, recentOnly)
}

// OtherQueryIDs lets the external tests read the id parameters a series is searched by.
func OtherQueryIDs(ids map[string]string) map[string]string { return otherQueryIDs(ids) }
