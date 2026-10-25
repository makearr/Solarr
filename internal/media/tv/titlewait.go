package tv

import (
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// TitleWaitAfterAiring is how long after it aired an episode still titled TBA is held from import (Sonarr's 48 hours).
const TitleWaitAfterAiring = 48 * time.Hour

// titleWaitError is a unit held because an episode in it has no title yet; the import retry places it once one has.
type titleWaitError struct{ refs []string }

func (e *titleWaitError) Error() string {
	return "waiting for the title of " + strings.Join(e.refs, ", ") + ", still TBA; at most 48 hours after it aired"
}

// IsTBA reports a title a provider has not given yet: empty or "TBA", as Sonarr reads it.
func IsTBA(title string) bool {
	title = strings.TrimSpace(title)
	return title == "" || strings.EqualFold(title, "TBA")
}

// airedAt is when each episode of a series aired, by media ref: its zoned time, else the start of its air date.
func airedAt(episodes []library.SeriesEpisode) map[string]time.Time {
	aired := make(map[string]time.Time, len(episodes))
	for _, episode := range episodes {
		ref := MediaRef(episode.Season, episode.Number)
		if episode.AvailableAt > 0 {
			aired[ref] = time.Unix(episode.AvailableAt, 0)
		} else if at, ok := airInstant(episode.Date); ok {
			aired[ref] = at
		}
	}
	return aired
}

// titleWait holds a unit an episode of which is still TBA and aired less than TitleWaitAfterAiring ago, as the
// library's Episode Title Required asks; an episode with no known air time is never held.
func titleWait(required settings.EpisodeTitleRequired, files []fileEpisodes, aired map[string]time.Time, now time.Time) error {
	bulk := len(files) > 1 || (len(files) == 1 && files[0].info.FullSeason)
	if required == settings.TitleRequiredNever || (required == settings.TitleRequiredBulkSeason && !bulk) {
		return nil
	}
	var waiting []string
	for _, file := range files {
		for _, part := range file.parts {
			at, known := aired[part.MediaRef]
			if IsTBA(part.Title) && known && now.Before(at.Add(TitleWaitAfterAiring)) {
				waiting = append(waiting, part.MediaRef)
			}
		}
	}
	if len(waiting) == 0 {
		return nil
	}
	return &titleWaitError{refs: waiting}
}
