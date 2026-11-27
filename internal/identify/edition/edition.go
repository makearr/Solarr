// Package edition chooses between the language editions of one title: the language the file itself shows first, then the
// languages the library prefers. Every media type with language editions (comics, books, audiobooks) asks it.
package edition

import (
	"regexp"
	"strings"
)

var names = map[string]string{
	"en": "en", "eng": "en", "english": "en", "fr": "fr", "fre": "fr", "fra": "fr", "french": "fr", "de": "de", "ger": "de", "deu": "de", "german": "de",
	"es": "es", "spa": "es", "spanish": "es", "it": "it", "ita": "it", "italian": "it", "pt": "pt", "por": "pt", "portuguese": "pt",
	"ja": "ja", "jpn": "ja", "jp": "ja", "japanese": "ja", "ko": "ko", "kor": "ko", "korean": "ko", "zh": "zh", "chi": "zh", "zho": "zh", "chinese": "zh",
	"ru": "ru", "rus": "ru", "russian": "ru", "nl": "nl", "dut": "nl", "nld": "nl", "dutch": "nl", "sv": "sv", "swe": "sv", "swedish": "sv",
	"pl": "pl", "pol": "pl", "polish": "pl", "da": "da", "dan": "da", "danish": "da", "no": "no", "nor": "no", "norwegian": "no", "fi": "fi", "fin": "fi", "finnish": "fi",
}

// Code is a language as ISO 639-1 ("en-US", "eng" and "English" are "en"); empty when it is not one this knows.
func Code(language string) string {
	lowered := strings.ToLower(strings.TrimSpace(language))
	if code, known := names[lowered]; known {
		return code
	}
	if primary, _, found := strings.Cut(lowered, "-"); found {
		return names[primary]
	}
	return ""
}

// taggedLanguage is a language written as a bracketed or dotted tag in a name: "[German]", "(fr)", ".ger.".
var taggedLanguage = regexp.MustCompile(`(?i)[\[(._ -](english|french|german|spanish|italian|portuguese|japanese|korean|chinese|russian|dutch|swedish|polish|danish|norwegian|finnish|eng|fre|fra|ger|deu|spa|ita|por|jpn|kor|rus|nl|fr|de|es|it|pt|ja|ko|ru)[\])._ -]`)

// FromName reads the language a file or folder name claims; empty when it claims none. A bare two-letter word counts only
// inside brackets, so "It" in a title is not Italian.
func FromName(name string) string {
	padded := " " + name + " "
	for _, match := range taggedLanguage.FindAllStringSubmatchIndex(padded, -1) {
		word := padded[match[2]:match[3]]
		opener, closer := padded[match[0]], padded[match[1]-1]
		bracketed := opener == '[' && closer == ']' || opener == '(' && closer == ')'
		if len(word) <= 3 && !bracketed {
			continue
		}
		if code := Code(word); code != "" {
			return code
		}
	}
	return ""
}

// Narrow keeps the editions of one title that are in the wanted language: the file's own, else the first of the
// preferred languages any edition is in. It never narrows to nothing, and editions of unknown language never match.
func Narrow[T any](editions []T, languageOf func(T) string, file string, preferred []string) []T {
	if len(editions) < 2 {
		return editions
	}
	for _, wanted := range append([]string{file}, preferred...) {
		if wanted == "" {
			continue
		}
		var matching []T
		for _, one := range editions {
			if Code(languageOf(one)) == wanted {
				matching = append(matching, one)
			}
		}
		if len(matching) > 0 {
			return matching
		}
	}
	return editions
}
