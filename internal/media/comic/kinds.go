package comic

import (
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// Library kind keys, stable across releases: a library stores one.
const (
	KindComics      = "comics"
	KindManga       = "manga"
	KindAdultComics = "adult_comics"
	KindHentai      = "hentai"
)

// Quality profile names a library of each kind is suggested; the profiles ship with the quality step.
var (
	comicProfiles = []string{"CBZ preferred", "Any comic"}
	mangaProfiles = []string{"CBZ preferred", "Any comic"}
)

// mangaRules suggest the manga library for a series the providers say is Japanese.
var mangaRules = []settings.LibraryRule{
	{Conditions: []settings.RuleCondition{{Field: settings.RuleLanguage, Value: "ja"}}},
}

// Kinds are what a library of the type is for, in the order the wizard offers them.
func Kinds() []media.LibraryKind {
	return []media.LibraryKind{
		{Key: KindComics, Label: "Comics", Help: "Western comics: series of issues, read left to right.", QualityProfiles: comicProfiles},
		{Key: KindManga, Label: "Manga", Help: "Series of tankobon volumes, read right to left; a chapter file counts toward its volume.", QualityProfiles: mangaProfiles, Rules: mangaRules},
		{Key: KindAdultComics, Label: "Adult comics", Help: "Comics for adults, hidden and confirmed as the adult type is.", Adult: true, QualityProfiles: comicProfiles},
		{Key: KindHentai, Label: "Hentai", Help: "Adult manga and one-shot galleries, hidden and confirmed as the adult type is.", Adult: true, QualityProfiles: mangaProfiles},
	}
}

// ProviderKeys are the metadata providers a library of the kind asks first, in order.
func ProviderKeys(kind string) []string {
	switch kind {
	case KindManga, KindHentai:
		return []string{"anilist", "mangaupdates", "mangadex"}
	default:
		return []string{"comicvine", "metron", "gcd"}
	}
}
