package match

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// unfoldedLetters are the letters that carry no combining mark to drop, spelled the way an ASCII catalogue does.
var unfoldedLetters = strings.NewReplacer(
	"ß", "ss", "æ", "ae", "œ", "oe", "ø", "o", "đ", "d", "ð", "d", "þ", "th", "ł", "l", "ı", "i", "ħ", "h", "ŧ", "t", "ĸ", "k",
)

// foldAccents decomposes the text (so "o" + U+0308 and "ö" read alike), drops the combining marks, then spells the
// letters that do not decompose.
func foldAccents(s string) string {
	decomposed := norm.NFKD.String(s)
	return unfoldedLetters.Replace(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, decomposed))
}

// trailingArticleSuffixes catches "Tonhut, The" so it normalizes the same as
// "The Example" -- section 10.2's own leading-article example.
var trailingArticleSuffixes = []string{", the", ", an", ", a"}

func moveTrailingArticle(s string) string {
	for _, suffix := range trailingArticleSuffixes {
		if strings.HasSuffix(s, suffix) {
			article := strings.TrimPrefix(suffix, ", ")
			return article + " " + strings.TrimSuffix(s, suffix)
		}
	}
	return s
}

func stripPunctuation(s string) string {
	return strings.Map(func(r rune) rune {
		// An apostrophe joins its word ("Shep's" is "sheps"), whichever of the look-alikes a tagger typed.
		if r == '\'' || r == '\u2019' || r == '\u2018' || r == '`' {
			return -1
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			return r
		}
		return ' '
	}, s)
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normalizeTitle applies section 10.2's normalisation: case fold, accent fold,
// leading-article handling, punctuation strip, separator collapse. Roman-numeral
// normalisation and embedded-year extraction are deliberately skipped -- Year is
// already its own Identity field, and folding "II" against "2" is disproportionate
// for this pass.
func normalizeTitle(s string) string {
	s = strings.ToLower(s)
	s = foldAccents(s)
	s = strings.TrimSpace(s)
	s = moveTrailingArticle(s)
	s = strings.ReplaceAll(s, "&", " and ")
	s = stripPunctuation(s)
	return collapseSpaces(s)
}

func tokenSet(s string) map[string]struct{} {
	tokens := strings.Fields(s)
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		set[token] = struct{}{}
	}
	return set
}

// jaccardWords is the token-set half of "token-set and Jaro-Winkler, best of" --
// word-overlap similarity, insensitive to word order.
func jaccardWords(a, b string) float64 {
	setA, setB := tokenSet(a), tokenSet(b)
	if len(setA) == 0 && len(setB) == 0 {
		return 1
	}
	var intersect int
	for token := range setA {
		if _, ok := setB[token]; ok {
			intersect++
		}
	}
	union := len(setA) + len(setB) - intersect
	if union == 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}

// jaroSimilarity is the standard Jaro string metric: characters matched within a
// window proportional to string length, transpositions counted and halved.
func jaroSimilarity(a, b string) float64 {
	runesA, runesB := []rune(a), []rune(b)
	if len(runesA) == 0 && len(runesB) == 0 {
		return 1
	}
	if len(runesA) == 0 || len(runesB) == 0 {
		return 0
	}

	matchDistance := max(len(runesA), len(runesB))/2 - 1
	if matchDistance < 0 {
		matchDistance = 0
	}

	matchedA := make([]bool, len(runesA))
	matchedB := make([]bool, len(runesB))
	var matches int
	for i := range runesA {
		lo := max(0, i-matchDistance)
		hi := min(len(runesB)-1, i+matchDistance)
		for j := lo; j <= hi; j++ {
			if matchedB[j] || runesA[i] != runesB[j] {
				continue
			}
			matchedA[i] = true
			matchedB[j] = true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0
	}

	var transpositions int
	k := 0
	for i := range runesA {
		if !matchedA[i] {
			continue
		}
		for !matchedB[k] {
			k++
		}
		if runesA[i] != runesB[k] {
			transpositions++
		}
		k++
	}

	m := float64(matches)
	return (m/float64(len(runesA)) + m/float64(len(runesB)) + (m-float64(transpositions)/2)/m) / 3
}

// jaroWinkler boosts jaroSimilarity for a shared prefix (up to four runes): two
// titles opening the same way are more likely the same release than the base
// Jaro score alone suggests.
func jaroWinkler(a, b string) float64 {
	jaro := jaroSimilarity(a, b)
	runesA, runesB := []rune(a), []rune(b)
	prefix := 0
	for prefix < len(runesA) && prefix < len(runesB) && prefix < 4 && runesA[prefix] == runesB[prefix] {
		prefix++
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}
