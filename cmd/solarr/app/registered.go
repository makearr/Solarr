package app

import (
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
)

// registeredMedia is the one list of media types in the binary; a new type is one line here.
func registeredMedia() (*media.Registry, error) {
	return media.NewRegistry(music.Module{}, tv.Module{})
}
