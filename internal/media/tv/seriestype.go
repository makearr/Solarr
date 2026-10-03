package tv

import "strings"

// dailyGenres are the genres whose shows air by date rather than by season and episode.
var dailyGenres = map[string]bool{"talk show": true, "news": true, "soap": true, "game show": true}

// DetectType reads what kind of series this is from what the providers say: anime is a Japanese series that AniList,
// AniDB or MyAnimeList list; daily is a talk, news, soap or game show; everything else is standard.
func DetectType(series Series) SeriesType {
	japanese := false
	for _, code := range []string{series.OriginalLanguage, series.OriginalCountry} {
		switch strings.ToLower(code) {
		case "ja", "jpn", "jp":
			japanese = true
		}
	}
	if japanese && len(series.IDs.AniList)+len(series.IDs.AniDB)+len(series.IDs.MAL) > 0 {
		return SeriesAnime
	}
	for _, genre := range series.Genres {
		if dailyGenres[strings.ToLower(genre)] {
			return SeriesDaily
		}
	}
	return SeriesStandard
}

// Valid reports whether the type is one of the three.
func (t SeriesType) Valid() bool { return t == SeriesStandard || t == SeriesDaily || t == SeriesAnime }
