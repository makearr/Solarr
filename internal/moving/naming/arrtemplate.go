package naming

import (
	"regexp"
	"strings"
)

// ArrTranslation is an *Arr's naming template written in Solarr's syntax, and what could not be carried over.
type ArrTranslation struct {
	Template string
	// Dropped are the tokens, as the *Arr spelled them, that Solarr has no counterpart for; they are left out of Template.
	Dropped []string
	// Notes say what else changed: an affix or spec Solarr cannot spell.
	Notes []string
}

// arrToken is the *Arr's token grammar: optional prefix and suffix characters that show only when the token has a value,
// the name, and an optional :format.
var arrToken = regexp.MustCompile(`(?i)^([-._ \[(]*)([a-z0-9]+(?:[-._ ]+[a-z0-9]+)?)(?::([a-z0-9|+-]+))?([-._ )\]]*)$`)

// arrNumbered are the tokens whose :format is a zero-padding width; the rest take a length to cut at.
var arrNumbered = map[string]bool{"season": true, "episode": true, "absolute": true, "track": true, "medium": true, "part": true}

var (
	emptyParentheses = regexp.MustCompile(`\(\s*\)`)
	repeatedSpaces   = regexp.MustCompile(` {2,}`)
)

// TranslateArrTemplate rewrites an *Arr's template (Sonarr, Radarr, Readarr, Whisparr, Lidarr) into Solarr's: tokens known to
// the scheme stay, an affix that shows only when the token has a value becomes a [conditional], literal brackets are escaped,
// and a token with no counterpart is dropped and named.
func TranslateArrTemplate(spec string, known map[string]bool) ArrTranslation {
	var out strings.Builder
	var result ArrTranslation
	runes := []rune(spec)
	for i := 0; i < len(runes); {
		switch r := runes[i]; r {
		case '{':
			if i+1 < len(runes) && runes[i+1] == '{' {
				out.WriteString("{{")
				i += 2
				continue
			}
			end := indexRune(runes, i+1, '}')
			if end < 0 {
				out.WriteString("{{")
				i++
				continue
			}
			body := string(runes[i+1 : end])
			i = end + 1
			writeArrToken(&out, &result, body, known)
		case '}':
			if i+1 < len(runes) && runes[i+1] == '}' {
				i++
			}
			out.WriteString("}}")
			i++
		case '[':
			out.WriteString("[[")
			i++
		case ']':
			out.WriteString("]]")
			i++
		default:
			out.WriteRune(r)
			i++
		}
	}
	result.Template = out.String()
	if len(result.Dropped) > 0 {
		result.Template = tidyAfterDrop(result.Template)
	}
	return result
}

func writeArrToken(out *strings.Builder, result *ArrTranslation, body string, known map[string]bool) {
	spelled := "{" + body + "}"
	parts := arrToken.FindStringSubmatch(body)
	if parts == nil {
		result.Dropped = append(result.Dropped, spelled)
		return
	}
	prefix, name, format, suffix := parts[1], parts[2], parts[3], parts[4]
	if !knownFold(known, name) && !knownFold(known, spacedName(name)) {
		result.Dropped = append(result.Dropped, "{"+name+"}")
		return
	}
	token := name
	if format != "" {
		switch {
		case isAllDigits(format) && arrNumbered[strings.ToLower(name)]:
			token += ":" + format
		case isAllDigits(format):
			token += ":." + format
		default:
			result.Notes = append(result.Notes, spelled+": the format :"+format+" is not carried over")
		}
	}
	if strings.ContainsAny(prefix+suffix, "[]") {
		result.Notes = append(result.Notes, spelled+": the square brackets around it are not carried over")
		prefix, suffix = withoutBrackets(prefix), withoutBrackets(suffix)
	}
	if prefix == "" && suffix == "" {
		out.WriteString("{" + token + "}")
		return
	}
	out.WriteString("[" + prefix + "{" + token + "}" + suffix + "]")
}

// withoutBrackets drops [ and ] from an affix, which Solarr cannot spell inside a conditional; parentheses stay.
func withoutBrackets(affix string) string {
	return strings.NewReplacer("[", "", "]", "").Replace(affix)
}

// tidyAfterDrop removes what a dropped token leaves behind: empty parentheses, doubled spaces, a space against a folder slash, a dangling separator at the end.
func tidyAfterDrop(template string) string {
	template = emptyParentheses.ReplaceAllString(template, "")
	template = repeatedSpaces.ReplaceAllString(template, " ")
	template = strings.NewReplacer("/ ", "/", " /", "/").Replace(template)
	return strings.TrimRight(template, " -_.")
}

// spacedName is a token spelled with a word separator ({Artist.Name}) as the scheme names it, with spaces.
func spacedName(name string) string {
	if separator, spelled := spelledWordSeparator(name); spelled {
		return strings.ReplaceAll(name, string(separator), " ")
	}
	return name
}
