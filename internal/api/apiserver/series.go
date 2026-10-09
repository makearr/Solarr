package apiserver

import "errors"

// ErrNotBuiltForMediaType is a feature the media type has no engine for yet; answered as 409 with the reason.
var ErrNotBuiltForMediaType = errors.New("apiserver: not built for this media type")

// ErrInvalidChoice is a value the request named that the series cannot take (an unknown monitor option, a profile of
// another media type); answered as 400 with the reason.
var ErrInvalidChoice = errors.New("apiserver: invalid choice")

// ErrSeriesNotFound is a series id that is not in the library asked about; answered as 404.
var ErrSeriesNotFound = errors.New("apiserver: series not found")

// ErrSeriesSourceUnavailable means no metadata provider could be reached; answered as 502.
var ErrSeriesSourceUnavailable = errors.New("apiserver: the metadata sources could not be reached")
