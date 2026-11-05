package parsetv

import (
	"path"
	"strings"
)

// SubtitleInfo is what a subtitle file's own name claims: its language and flags.
type SubtitleInfo struct {
	Language        string
	Forced          bool
	HearingImpaired bool
}

var subtitleExtensions = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".vtt": true, ".idx": true, ".sup": true}

// subtitleLanguages maps the codes and names a subtitle file carries between the title and its extension.
var subtitleLanguages = map[string]string{
	"en": "en", "eng": "en", "english": "en", "de": "de", "ger": "de", "deu": "de", "german": "de",
	"fr": "fr", "fre": "fr", "fra": "fr", "french": "fr", "es": "es", "spa": "es", "spanish": "es",
	"it": "it", "ita": "it", "italian": "it", "nl": "nl", "dut": "nl", "nld": "nl", "dutch": "nl",
	"pt": "pt", "por": "pt", "portuguese": "pt", "pt-br": "pt-BR", "ptbr": "pt-BR", "ru": "ru", "rus": "ru",
	"ja": "ja", "jpn": "ja", "japanese": "ja", "ko": "ko", "kor": "ko", "zh": "zh", "chi": "zh", "zho": "zh",
	"pl": "pl", "pol": "pl", "sv": "sv", "swe": "sv", "da": "da", "dan": "da", "no": "no", "nor": "no",
	"fi": "fi", "fin": "fi", "tr": "tr", "tur": "tr", "cs": "cs", "cze": "cs", "ces": "cs", "hu": "hu", "hun": "hu",
	"ar": "ar", "ara": "ar", "he": "he", "heb": "he", "el": "el", "gre": "el", "ell": "el",
}

// ParseSubtitleFile reads the language and flags from names like "Show.S01E01.de.forced.srt"; ok is false for a non-subtitle file.
func ParseSubtitleFile(name string) (SubtitleInfo, bool) {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	extension := strings.ToLower(path.Ext(name))
	if !subtitleExtensions[extension] {
		return SubtitleInfo{}, false
	}
	tokens := strings.FieldsFunc(strings.TrimSuffix(name, path.Ext(name)), func(r rune) bool { return r == '.' || r == '_' || r == ' ' })
	var info SubtitleInfo
	// Only the trailing tokens are claims; a language word inside the title stays part of the title.
	for i := len(tokens) - 1; i >= 1; i-- {
		token := strings.ToLower(tokens[i])
		switch token {
		case "forced":
			info.Forced = true
			continue
		case "sdh", "cc", "hi":
			info.HearingImpaired = true
			continue
		case "default":
			continue
		}
		if code, found := subtitleLanguages[token]; found && info.Language == "" {
			info.Language = code
			continue
		}
		break
	}
	return info, true
}

// CanonicalSubtitleSuffix rewrites what follows a video's name in a subtitle file (".eng.FORCED.srt") with the two-letter
// language and lower-case flags (".en.forced.srt"); tokens that claim nothing stay as written.
func CanonicalSubtitleSuffix(suffix string) string {
	extension := path.Ext(suffix)
	if !subtitleExtensions[strings.ToLower(extension)] {
		return suffix
	}
	middle := strings.TrimPrefix(strings.TrimSuffix(suffix, extension), ".")
	if middle == "" {
		return suffix
	}
	tokens := strings.Split(middle, ".")
	for i, token := range tokens {
		lower := strings.ToLower(token)
		switch lower {
		case "forced", "sdh", "cc", "hi", "default":
			tokens[i] = lower
			continue
		}
		if code, found := subtitleLanguages[lower]; found {
			tokens[i] = code
		}
	}
	return "." + strings.Join(tokens, ".") + strings.ToLower(extension)
}
