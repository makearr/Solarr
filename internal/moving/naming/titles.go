package naming

import (
	"regexp"
	"strings"
	"unicode"
)

// Lidarr's derived title forms, shared by every media type that names a title.

var leadingArticle = regexp.MustCompile(`^(The|An|A) (.*?)((?: \(\d{4}\))?)$`)

// TitleThe moves a leading article to the end, keeping a trailing year: "The Kokopob" is
// "Kokopob, The".
func TitleThe(title string) string {
	return leadingArticle.ReplaceAllString(title, "$2, $1$3")
}

// cleanTitleRemoved is the punctuation Lidarr's CleanTitle drops.
const cleanTitleRemoved = "'’`:?,!()[]{}\"<>|;~@$%^*="

// CleanTitle is Lidarr's search-friendly title: "&" spelled out, slashes to spaces,
// punctuation dropped, a lone dash or underscore between words removed.
func CleanTitle(title string) string {
	title = strings.ReplaceAll(title, "&", "and")
	title = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return ' '
		case strings.ContainsRune(cleanTitleRemoved, r):
			return -1
		default:
			return r
		}
	}, title)
	words := strings.Fields(title)
	kept := words[:0]
	for _, word := range words {
		if word != "-" && word != "_" {
			kept = append(kept, word)
		}
	}
	return strings.Join(kept, " ")
}

// FirstCharacter is the upper-cased first letter or digit of TitleThe(title), "_" when
// neither of the first two characters is one -- Lidarr's {Artist NameFirstCharacter}.
func FirstCharacter(title string) string {
	runes := []rune(TitleThe(title))
	for i := 0; i < len(runes) && i < 2; i++ {
		if unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) {
			return strings.ToUpper(string(runes[i]))
		}
	}
	return "_"
}
