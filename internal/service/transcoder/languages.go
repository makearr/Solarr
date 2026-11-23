package transcoder

import "github.com/makearr/Solarr/internal/db/settings"

// Languages are the two-letter codes a library wants, in order of preference; empty means no stream is removed for its language.
type Languages struct {
	Audio     []string
	Subtitles []string
}

// WantedLanguages is the target's lists, else the library's language for audio; subtitles follow only the target.
func WantedLanguages(target settings.QualityTarget, libraryLanguages []string) Languages {
	wanted := Languages{Audio: target.AudioLanguages, Subtitles: target.SubtitleLanguages}
	if len(wanted.Audio) == 0 {
		wanted.Audio = libraryLanguages
	}
	return wanted
}
