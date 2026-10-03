package tv

import (
	"fmt"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/identify/parsetv"
)

// ReleaseLabel is what a release holds, for a queue or history row: "S03E10", "S03E01-E04", the season of a pack or
// an air date, falling back to the season item's own reference.
func ReleaseLabel(releaseTitle, seasonRef string, now time.Time) string {
	fallback := strings.ToUpper(seasonRef)
	info, ok := parsetv.Parse(releaseTitle, now)
	if !ok {
		return fallback
	}
	switch {
	case len(info.EpisodeNumbers) == 1:
		return fmt.Sprintf("S%02dE%02d", info.SeasonNumber, info.EpisodeNumbers[0])
	case len(info.EpisodeNumbers) > 1:
		last := info.EpisodeNumbers[len(info.EpisodeNumbers)-1]
		return fmt.Sprintf("S%02dE%02d-E%02d", info.SeasonNumber, info.EpisodeNumbers[0], last)
	case info.AirDate != "":
		return info.AirDate
	case info.FullSeason && info.SeasonNumber > 0:
		return fmt.Sprintf("S%02d", info.SeasonNumber)
	}
	return fallback
}
