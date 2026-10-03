package mediatool

import "strings"

// languageCodes maps the ISO 639-2 spellings taggers write to the two-letter codes profiles use.
var languageCodes = map[string]string{
	"eng": "en", "deu": "de", "ger": "de", "fra": "fr", "fre": "fr", "spa": "es", "ita": "it", "jpn": "ja",
	"por": "pt", "nld": "nl", "dut": "nl", "rus": "ru", "swe": "sv", "nor": "no", "dan": "da", "fin": "fi",
	"pol": "pl", "kor": "ko", "zho": "zh", "chi": "zh", "tur": "tr", "ces": "cs", "cze": "cs", "hun": "hu",
	"ell": "el", "gre": "el",
}

// undetermined are the codes that say no language is stated.
var undetermined = map[string]bool{"und": true, "zxx": true, "mul": true, "xxx": true, "": true}

// normaliseLanguage answers a language tag as a two-letter code, or "" when none is stated.
func normaliseLanguage(raw string) string {
	code := strings.ToLower(strings.TrimSpace(raw))
	if undetermined[code] {
		return ""
	}
	if two, known := languageCodes[code]; known {
		return two
	}
	return code
}

// LanguageCode answers a language tag or file-name token as a two-letter code, or "" when it is not one that says a language.
func LanguageCode(raw string) string {
	code := normaliseLanguage(raw)
	if len(code) == 2 || len(code) == 3 {
		return code
	}
	return ""
}

// lyricsLanguages reads the language the lyrics are in: ID3's USLT shows up as "lyrics-<code>", a Vorbis
// comment as LYRICS beside a LANGUAGE tag.
func lyricsLanguages(tags Tags) []string {
	var found []string
	add := func(raw string) {
		if code := normaliseLanguage(raw); code != "" && !contains(found, code) {
			found = append(found, code)
		}
	}
	hasLyrics := false
	for key, value := range tags {
		if value == "" {
			continue
		}
		for _, prefix := range []string{"lyrics-", "unsyncedlyrics-", "unsynced lyrics-"} {
			if strings.HasPrefix(key, prefix) {
				hasLyrics = true
				add(strings.TrimPrefix(key, prefix))
			}
		}
		if key == "lyrics" || key == "unsyncedlyrics" {
			hasLyrics = true
		}
	}
	if hasLyrics && len(found) == 0 {
		add(tags["language"])
	}
	return found
}

// audioLanguages reads the language an audio stream or the file declares.
func audioLanguages(streamLanguages []string, tags Tags) []string {
	var found []string
	for _, raw := range append(streamLanguages, tags["language"]) {
		if code := normaliseLanguage(raw); code != "" && !contains(found, code) {
			found = append(found, code)
		}
	}
	return found
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
