package parsetv

import (
	"strings"

	"github.com/dlclark/regexp2"
)

// OriginalLanguage is the claim "dubbed into nothing": the show's own audio.
const OriginalLanguage = "orig"

var (
	cleanLanguageTitleRe = compile(`.*?[_. ](S\d{2}(?:E\d{2,4})*[_. ].*)`, regexp2.IgnoreCase)
	languageRe           = compile(`(?:\W|_)(?<english>\b(?:ing|eng)\b)|(?<italian>\b(?:ita|italian)\b)|(?<german>german\b|videomann|ger[. ]dub)|(?<flemish>flemish)|(?<greek>greek)|(?<french>(?:\W|_)(?:FR|VF|VF2|VFF|VFI|VFQ|TRUEFRENCH|FRENCH)(?:\W|_))|(?<russian>\b(?:rus|ru)\b)|(?<hungarian>\b(?:HUNDUB|HUN)\b)|(?<hebrew>\bHebDub\b)|(?<polish>\b(?:PL\W?DUB|DUB\W?PL|LEK\W?PL|PL\W?LEK)\b)|(?<chinese>\[(?:CH[ST]|BIG5|GB)\]|简|繁|字幕)|(?<bulgarian>\bbgaudio\b)|(?<spanish>\b(?:español|castellano|esp|spa(?!\(Latino\)))\b)|(?<ukrainian>\b(?:\dx?)?(?:ukr))|(?<thai>\b(?:THAI)\b)|(?<romanian>\b(?:RoDubbed|ROMANIAN)\b)|(?<catalan>[-,. ]cat[. ](?:DD|subs)|\b(?:catalan|catalán)\b)|(?<latvian>\b(?:lat|lav|lv)\b)|(?<turkish>\b(?:tur)\b)|(?<original>\b(?:orig|original)\b)`, regexp2.IgnoreCase)
	caseSensitiveLangRe  = compile(`(?:(?i)(?<!SUB[\W|_|^]))(?:(?<lithuanian>\bLT\b)|(?<czech>\bCZ\b)|(?<polish>\bPL\b)|(?<bulgarian>\bBG\b)|(?<slovak>\bSK\b))(?:(?i)(?![\W|_|^]SUB))`, 0)
	germanDualRe         = compile(`(?<!WEB[-_. ]?)\bDL\b`, regexp2.IgnoreCase)
	germanMultiRe        = compile(`\bML\b`, regexp2.IgnoreCase)
)

// languageWords are the spelled-out names a release may carry, in the order Sonarr checks them.
var languageWords = []struct{ word, code string }{
	{"spanish", "es"}, {"danish", "da"}, {"dutch", "nl"}, {"japanese", "ja"}, {"icelandic", "is"},
	{"mandarin", "zh"}, {"cantonese", "zh"}, {"chinese", "zh"}, {"korean", "ko"}, {"russian", "ru"},
	{"polish", "pl"}, {"vietnamese", "vi"}, {"swedish", "sv"}, {"norwegian", "no"}, {"finnish", "fi"},
	{"turkish", "tr"}, {"portuguese", "pt"}, {"hungarian", "hu"}, {"hebrew", "he"}, {"arabic", "ar"},
	{"hindi", "hi"}, {"malayalam", "ml"}, {"ukrainian", "uk"}, {"bulgarian", "bg"}, {"slovak", "sk"},
	{"brazilian", "pt-BR"}, {"dublado", "pt-BR"}, {"latino", "es-419"}, {"latvian", "lv"},
}

var languageGroupCodes = map[string]string{
	"english": "en", "italian": "it", "german": "de", "flemish": "nl-BE", "greek": "el", "french": "fr",
	"russian": "ru", "hungarian": "hu", "hebrew": "he", "polish": "pl", "chinese": "zh", "bulgarian": "bg",
	"spanish": "es", "ukrainian": "uk", "thai": "th", "romanian": "ro", "catalan": "ca", "latvian": "lv",
	"turkish": "tr", "original": OriginalLanguage, "lithuanian": "lt", "czech": "cs", "slovak": "sk",
}

var languageGroupOrder = []string{
	"english", "italian", "german", "flemish", "greek", "french", "russian", "hungarian", "hebrew", "polish",
	"chinese", "bulgarian", "spanish", "ukrainian", "thai", "romanian", "catalan", "latvian", "turkish", "original",
}

// ParseLanguages reads the audio language claims of a release's tokens; empty means the name says nothing.
func ParseLanguages(title string) []string {
	if m := firstMatch(cleanLanguageTitleRe, title); m != nil {
		title, _ = groupValue(m, "1")
	}
	lower := strings.ToLower(title)

	var languages []string
	add := func(code string) {
		for _, have := range languages {
			if have == code {
				return
			}
		}
		languages = append(languages, code)
	}
	for _, entry := range languageWords {
		if strings.Contains(lower, entry.word) {
			add(entry.code)
		}
	}
	for _, code := range regexLanguages(title) {
		add(code)
	}
	if strings.Contains(lower, "english") {
		add("en")
	}

	if len(languages) == 1 && languages[0] == "de" {
		if isMatch(germanDualRe, title) {
			add(OriginalLanguage)
		} else if isMatch(germanMultiRe, title) {
			add(OriginalLanguage)
			add("en")
		}
	}
	return languages
}

func regexLanguages(title string) []string {
	var codes []string
	for _, m := range allMatches(languageRe, title) {
		for _, name := range languageGroupOrder {
			if _, found := groupValue(m, name); found {
				codes = append(codes, languageGroupCodes[name])
			}
		}
	}
	for _, m := range allMatches(caseSensitiveLangRe, title) {
		for _, name := range []string{"lithuanian", "czech", "polish", "bulgarian", "slovak"} {
			if _, found := groupValue(m, name); found {
				codes = append(codes, languageGroupCodes[name])
			}
		}
	}
	return codes
}
