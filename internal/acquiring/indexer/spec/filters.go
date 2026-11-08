package spec

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// knownFilters are the filters the engine runs; a spec using another cannot be searched.
var knownFilters = map[string]bool{
	"dateparse": true, "append": true, "querystring": true, "replace": true, "re_replace": true, "regexp": true,
	"andmatch": true, "prepend": true, "timeago": true, "trim": true, "split": true, "fuzzytime": true, "validate": true,
	"validfilename": true, "htmldecode": true, "urldecode": true, "diacritics": true, "urlencode": true, "toupper": true,
	"tolower": true, "timeparse": true, "jsonjoinarray": true,
}

// filterContext is what some filters need besides the value.
type filterContext struct {
	variables *Variables
	now       time.Time
}

// applyFilters runs the filters in order. An argument is a template, so it can name settings and earlier fields.
func applyFilters(value string, filters []Filter, context filterContext) (string, error) {
	for _, filter := range filters {
		args := make([]string, len(filter.Args))
		for i, arg := range filter.Args {
			rendered, err := render(arg, context.variables)
			if err != nil {
				return "", err
			}
			args[i] = rendered
		}
		var err error
		if value, err = applyFilter(value, filter.Name, args, context); err != nil {
			return "", err
		}
	}
	return value, nil
}

func arg(args []string, at int) string {
	if at < len(args) {
		return args[at]
	}
	return ""
}

func applyFilter(value, name string, args []string, context filterContext) (string, error) {
	switch name {
	case "append":
		return value + arg(args, 0), nil
	case "prepend":
		return arg(args, 0) + value, nil
	case "replace":
		return strings.ReplaceAll(value, arg(args, 0), arg(args, 1)), nil
	case "re_replace":
		compiled, err := regexp.Compile(arg(args, 0))
		if err != nil {
			return "", fmt.Errorf("spec: re_replace %q: %w", arg(args, 0), err)
		}
		return compiled.ReplaceAllString(value, dotNetReplacement(arg(args, 1))), nil
	case "regexp":
		compiled, err := regexp.Compile(arg(args, 0))
		if err != nil {
			return "", fmt.Errorf("spec: regexp %q: %w", arg(args, 0), err)
		}
		match := compiled.FindStringSubmatch(value)
		switch {
		case match == nil:
			return "", nil
		case len(match) == 1:
			return match[0], nil
		}
		for _, group := range match[1:] {
			if group != "" {
				return group, nil
			}
		}
		return "", nil
	case "trim":
		if cutset := arg(args, 0); cutset != "" {
			return strings.Trim(value, cutset), nil
		}
		return strings.TrimSpace(value), nil
	case "split":
		parts := strings.Split(value, arg(args, 0))
		index, _ := strconv.Atoi(arg(args, 1))
		if index < 0 {
			index += len(parts)
		}
		if index < 0 || index >= len(parts) {
			return "", nil
		}
		return parts[index], nil
	case "querystring":
		query := value
		if at := strings.Index(value, "?"); at >= 0 {
			query = value[at+1:]
		}
		if parsed, err := url.ParseQuery(query); err == nil {
			return parsed.Get(arg(args, 0)), nil
		}
		return "", nil
	case "htmldecode":
		return html.UnescapeString(value), nil
	case "urldecode":
		if decoded, err := url.QueryUnescape(value); err == nil {
			return decoded, nil
		}
		return value, nil
	case "urlencode":
		return url.QueryEscape(value), nil
	case "toupper":
		return strings.ToUpper(value), nil
	case "tolower":
		return strings.ToLower(value), nil
	case "diacritics":
		if stripped, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), value); err == nil {
			return stripped, nil
		}
		return value, nil
	case "validfilename":
		return strings.Map(func(r rune) rune {
			if strings.ContainsRune(`\/:*?"<>|`, r) {
				return -1
			}
			return r
		}, value), nil
	case "validate":
		for _, allowed := range strings.Split(arg(args, 0), ",") {
			if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(value)) {
				return value, nil
			}
		}
		return "", nil
	case "andmatch":
		return andMatch(value, arg(args, 0), context.variables.Keywords), nil
	case "jsonjoinarray":
		return joinJSONArray(value, arg(args, 0), arg(args, 1)), nil
	case "dateparse", "timeparse":
		parsed, ok := parseDotNetDate(strings.TrimSpace(value), arg(args, 0), context.now)
		if !ok {
			return "", nil
		}
		return parsed.UTC().Format(time.RFC3339), nil
	case "timeago":
		if ago, ok := parseTimeAgo(value, context.now); ok {
			return ago.UTC().Format(time.RFC3339), nil
		}
		return "", nil
	case "fuzzytime":
		if parsed, ok := parseFuzzyTime(value, context.now); ok {
			return parsed.UTC().Format(time.RFC3339), nil
		}
		return "", nil
	}
	return "", fmt.Errorf("spec: unknown filter %q", name)
}

// andMatch keeps a title only when it holds every word of the search, so a loose site search does not flood the results.
func andMatch(title, limit, keywords string) string {
	words := strings.Fields(strings.ToLower(keywords))
	if most, err := strconv.Atoi(limit); err == nil && most > 0 && len(words) > most {
		words = words[:most]
	}
	lower := strings.ToLower(title)
	for _, word := range words {
		if !strings.Contains(lower, word) {
			return ""
		}
	}
	return title
}

func joinJSONArray(value, key, separator string) string {
	var items []any
	if err := json.Unmarshal([]byte(value), &items); err != nil {
		return value
	}
	var parts []string
	for _, item := range items {
		switch typed := item.(type) {
		case map[string]any:
			if text, ok := typed[strings.TrimPrefix(key, ".")]; ok {
				parts = append(parts, fmt.Sprint(text))
			}
		default:
			parts = append(parts, fmt.Sprint(typed))
		}
	}
	return strings.Join(parts, separator)
}
