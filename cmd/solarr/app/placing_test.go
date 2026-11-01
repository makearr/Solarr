package app

import (
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/adult"
	"github.com/makearr/Solarr/internal/media/book"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
)

// Review places a file through this capability: a module without it answers "not built for this media type";
// adult and book were built that way, their Files unused.
func TestEveryMediaTypeOfferedInReviewPlacesFiles(t *testing.T) {
	for name, module := range map[string]media.Module{
		"music": music.Module{}, "tv": tv.Module{}, "movie": movie.Module{}, "book": book.Module{}, "adult": adult.Module{},
	} {
		if _, placing := module.(media.PlacingFiles); !placing {
			t.Errorf("%s cannot place a file from Review", name)
		}
	}
}
