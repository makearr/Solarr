package tv

import (
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/screen"
)

// The kinds of TV library (T3): each is defaults only, and anime and daily also type their series (seriesTypeOfKind).
const (
	KindSeries      = "series"
	KindAnime       = "anime"
	KindDocumentary = "documentary"
	KindReality     = "reality"
	KindKids        = "kids"
	KindDaily       = "daily"
	KindSports      = "sports"
	KindNews        = "news"
	KindGameShow    = "game_show"
	KindTalkShow    = "talk_show"
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
		QualityProfiles: []string{"Anime 1080p", "WEB 1080p"}, Language: animeLanguages, Rules: screen.AnimeRules,
	},
	{
		Key: KindDocumentary, Label: "Documentary", Help: "Documentary series.",
		QualityProfiles: []string{"WEB 1080p", "HD 720p/1080p"}, Language: seriesLanguages, Rules: screen.DocumentaryRules,
	},
	{
		Key: KindReality, Label: "Reality", Help: "Reality and game shows, where a smaller file is plenty.",
		QualityProfiles: []string{"Space conscious (x265 1080p)", "HD 720p/1080p"}, Language: seriesLanguages, Rules: screen.RealityRules,
	},
	{
		Key: KindKids, Label: "Kids", Help: "Children's series.",
		QualityProfiles: []string{"HD 720p/1080p", "WEB 1080p"}, Language: seriesLanguages, Rules: screen.KidsRules,
	},
	{
		Key: KindDaily, Label: "Daily", Help: "Shows that air every day, of any sort: its series are daily, searched and named by air date.",
		QualityProfiles: []string{"HD 720p/1080p", "WEB 1080p"}, Language: seriesLanguages,
	},
	{
		Key: KindSports, Label: "Sports", Help: "Sports coverage and sports series.",
		QualityProfiles: []string{"HD 720p/1080p", "WEB 1080p"}, Language: seriesLanguages, Rules: media.GenreRules("Sports", "Sport"),
	},
	{
		Key: KindNews, Label: "News", Help: "News programmes and current affairs.",
		QualityProfiles: []string{"HD 720p/1080p", "WEB 1080p"}, Language: seriesLanguages, Rules: media.GenreRules("News"),
	},
	{
		Key: KindGameShow, Label: "Game show", Help: "Quiz and game shows.",
		QualityProfiles: []string{"HD 720p/1080p", "WEB 1080p"}, Language: seriesLanguages, Rules: media.GenreRules("Game Show"),
	},
	{
		Key: KindTalkShow, Label: "Talk show", Help: "Talk shows and late-night shows.",
		QualityProfiles: []string{"HD 720p/1080p", "WEB 1080p"}, Language: seriesLanguages, Rules: media.GenreRules("Talk", "Talk Show"),
	},
}

// seriesTypeOfKind is the type a series added to a library of the kind takes when the providers say standard.
func seriesTypeOfKind(kind string) SeriesType {
	switch kind {
	case KindAnime:
		return SeriesAnime
	case KindDaily:
		return SeriesDaily
	}
	return SeriesStandard
}
