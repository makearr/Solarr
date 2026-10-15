package tv

import (
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// The kinds of TV library (T3): each is defaults only, and anime also types its series (seriesTypeOfKind).
const (
	KindSeries      = "series"
	KindAnime       = "anime"
	KindDocumentary = "documentary"
	KindReality     = "reality"
	KindKids        = "kids"
)

// The language profiles the kinds start with (section 19.8: series en/de, anime ja/en/de); preferred, never required.
var (
	seriesLanguages = &settings.LanguageProfile{Name: "English or German", AudioPreferred: []string{"en", "de"}}
	animeLanguages  = &settings.LanguageProfile{
		Name: "Japanese, English or German", AudioPreferred: []string{"ja", "en", "de"}, SubtitlePreferred: []string{"en", "de"},
	}
)

var libraryKinds = []media.LibraryKind{
	{
		Key: KindSeries, Label: "Series", Help: "Shows and miniseries.",
		QualityProfiles: []string{"WEB 1080p", "HD 720p/1080p"}, Language: seriesLanguages,
	},
	{
		Key: KindAnime, Label: "Anime", Help: "Its series are anime: searched by absolute episode number as well and in the indexers' anime category.",
		QualityProfiles: []string{"Anime 1080p", "WEB 1080p"}, Language: animeLanguages,
	},
	{
		Key: KindDocumentary, Label: "Documentary", Help: "Documentary series.",
		QualityProfiles: []string{"WEB 1080p", "HD 720p/1080p"}, Language: seriesLanguages,
	},
	{
		Key: KindReality, Label: "Reality", Help: "Reality and game shows, where a smaller file is plenty.",
		QualityProfiles: []string{"Space conscious (x265 1080p)", "HD 720p/1080p"}, Language: seriesLanguages,
	},
	{
		Key: KindKids, Label: "Kids", Help: "Children's series.",
		QualityProfiles: []string{"HD 720p/1080p", "WEB 1080p"}, Language: seriesLanguages,
	},
}

// seriesTypeOfKind is the type a series added to a library of the kind takes when the providers say standard.
func seriesTypeOfKind(kind string) SeriesType {
	if kind == KindAnime {
		return SeriesAnime
	}
	return SeriesStandard
}
