// Package titlekey folds a title to the key two spellings of it share.
package titlekey

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Options say how far a fold goes beyond the lower case, accent folding and dropped apostrophes every fold does.
type Options struct {
	// Words keeps the words apart by one space; otherwise the key is letters and digits alone.
	Words bool
	// AndForAmpersand spells "&" as the word "and".
	AndForAmpersand bool
	// DropLeadingArticle drops a leading "the", "a" or "an" when a word follows.
	DropLeadingArticle bool
}

// Compact is a title's letters and digits alone, for finding one title inside another.
var Compact = Options{AndForAmpersand: true}

// Words is a title's words, for comparing two titles word by word.
var Words = Options{Words: true, AndForAmpersand: true}

// WordsNoArticle is Words without a leading article.
var WordsNoArticle = Options{Words: true, AndForAmpersand: true, DropLeadingArticle: true}

// unfoldedLetters are the letters that carry no combining mark to drop, spelled the way an ASCII catalogue does.
var unfoldedLetters = strings.NewReplacer(
	"ß", "ss", "æ", "ae", "œ", "oe", "ø", "o", "đ", "d", "ð", "d", "þ", "th", "ł", "l", "ı", "i", "ħ", "h", "ŧ", "t", "ĸ", "k",
)

// FoldAccents decomposes the text (so "o" + U+0308 and "ö" read alike), drops the combining marks, then spells the
// letters that do not decompose.
func FoldAccents(s string) string {
	return unfoldedLetters.Replace(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, norm.NFKD.String(s)))
}

// Fold is the key of title under o.
func Fold(title string, o Options) string {
	title = FoldAccents(strings.ToLower(title))
	if o.AndForAmpersand {
		title = strings.ReplaceAll(title, "&", " and ")
	}
	var key strings.Builder
	for _, r := range title {
		switch {
		case isApostrophe(r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			key.WriteRune(r)
		case o.Words:
			key.WriteRune(' ')
		}
	}
	if !o.Words {
		return key.String()
	}
	words := strings.Fields(key.String())
	if o.DropLeadingArticle && len(words) > 1 && (words[0] == "the" || words[0] == "a" || words[0] == "an") {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// isApostrophe says r joins its word ("Shep's" is "sheps"), whichever of the look-alikes a tagger typed.
func isApostrophe(r rune) bool { return r == '\'' || r == '’' || r == '‘' || r == '`' }
