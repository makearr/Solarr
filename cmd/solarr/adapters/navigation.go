package adapters

import (
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
)

// Navigation satisfies apiserver.NavigationProvider by dispatching on
// media type. Direct-wired to music.Navigation until stage 9 extracts media
// types as plugins (§2's "direct-wired, then refactored" convention) -- a
// second media type adds one more map entry here, not a branch in apiserver.
type Navigation struct{}

func (Navigation) LevelsFor(mediaType string) ([]model.Level, bool) {
	levels, ok := navigationByMediaType[mediaType]
	if !ok {
		return nil, false
	}
	return levels.Levels(), true
}

var navigationByMediaType = map[string]model.NavigationLevels{
	"music": music.Navigation{},
	"tv":    tv.Navigation{},
}
