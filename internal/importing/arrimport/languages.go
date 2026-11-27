package arrimport

import (
	"context"
	"strings"
)

// languageCodes are the *Arr language names Solarr can preselect, by ISO 639-1 code; "Any", "Original" and "Unknown" mean no preference.
var languageCodes = map[string]string{
	"english": "en", "german": "de", "french": "fr", "spanish": "es", "italian": "it", "dutch": "nl", "portuguese": "pt",
	"russian": "ru", "japanese": "ja", "korean": "ko", "chinese": "zh", "swedish": "sv", "norwegian": "no", "danish": "da",
	"finnish": "fi", "polish": "pl", "czech": "cs", "turkish": "tr", "greek": "el", "hebrew": "he", "arabic": "ar", "hindi": "hi",
	"hungarian": "hu", "ukrainian": "uk", "thai": "th", "vietnamese": "vi", "romanian": "ro", "icelandic": "is", "catalan": "ca",
}

// LanguageCode is the ISO 639-1 code of an *Arr's language name, empty for "Any", "Original" or one Solarr has no code for.
func LanguageCode(name string) string { return languageCodes[strings.ToLower(strings.TrimSpace(name))] }

// languageNamed is the language field of a Radarr quality profile or the cutoff of a Sonarr language profile.
type languageNamed struct {
	Name string `json:"name"`
}

// unique adds the codes of names to codes in order, each once.
func unique(codes []string, names ...string) []string {
	for _, name := range names {
		if code := LanguageCode(name); code != "" && !contains(codes, code) {
			codes = append(codes, code)
		}
	}
	return codes
}

func contains(list []string, item string) bool {
	for _, one := range list {
		if one == item {
			return true
		}
	}
	return false
}

// Languages are the languages the instance's quality profiles ask for, in profile order: the language of each Radarr
// quality profile. Empty for the many profiles that take any.
func (c RadarrClient) Languages(ctx context.Context) ([]string, error) {
	var profiles []struct {
		Language languageNamed `json:"language"`
	}
	if err := c.inner.get(ctx, "/api/v3/qualityprofile", &profiles); err != nil {
		return nil, err
	}
	var codes []string
	for _, profile := range profiles {
		codes = unique(codes, profile.Language.Name)
	}
	return codes, nil
}

// Languages are the cutoff languages of Sonarr v3's language profiles, in profile order; Sonarr v4 has none and the call fails.
func (c SonarrClient) Languages(ctx context.Context) ([]string, error) {
	var profiles []struct {
		Cutoff languageNamed `json:"cutoff"`
	}
	if err := c.inner.get(ctx, "/api/v3/languageprofile", &profiles); err != nil {
		return nil, err
	}
	var codes []string
	for _, profile := range profiles {
		codes = unique(codes, profile.Cutoff.Name)
	}
	return codes, nil
}
