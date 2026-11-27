package music

import (
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// libraryKinds are what a music library is for, so a new album is suggested to the right one (plan 10-libraries L5).
var libraryKinds = []media.LibraryKind{
	{Key: "general", Label: "General", Help: "Albums of any genre.", QualityProfiles: []string{"Lossless preferred", "Lossy 320", "Space conscious"}},
	{
		Key: "classical", Label: "Classical", Help: "Classical, opera and contemporary classical.", QualityProfiles: []string{"Lossless preferred", "Lossy 320"},
		Rules: []settings.LibraryRule{{Conditions: []settings.RuleCondition{{Field: settings.RuleGenre, Value: "Classical"}}}},
	},
	{
		Key: "soundtracks", Label: "Soundtracks", Help: "Scores and soundtracks of films, series and games.", QualityProfiles: []string{"Lossless preferred", "Lossy 320"},
		Rules: []settings.LibraryRule{{Conditions: []settings.RuleCondition{{Field: settings.RuleGenre, Value: "Soundtrack"}}}},
	},
	{
		Key: "kids", Label: "Kids", Help: "Children's music.", QualityProfiles: []string{"Lossy 320", "Space conscious"},
		Rules: []settings.LibraryRule{{Conditions: []settings.RuleCondition{{Field: settings.RuleGenre, Value: "Children's Music"}}}},
	},
	{Key: "live", Label: "Live and DJ mixes", Help: "Live recordings, DJ mixes and mixtapes.", QualityProfiles: []string{"Lossy 320", "Lossless preferred", "Space conscious"}},
}
