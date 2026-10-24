package movie

import (
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/screen"
)

// The kinds of movie library: what a library of films is for, so a new film is suggested to the right one
// (plan 10-libraries L5); they bring no profiles of their own.
var libraryKinds = []media.LibraryKind{
	{Key: "movies", Label: "Movies", Help: "Films."},
	{
		Key: "anime", Label: "Anime", Help: "Anime films.", Rules: screen.AnimeRules,
		Language: &settings.LanguageProfile{Name: "Japanese, English or German", AudioPreferred: []string{"ja", "en", "de"}, SubtitlePreferred: []string{"en", "de"}},
	},
	{Key: "documentary", Label: "Documentary", Help: "Documentary films.", Rules: screen.DocumentaryRules},
	{Key: "kids", Label: "Kids", Help: "Children's and family films.", Rules: screen.KidsRules},
}
