// Package parseadult reads what a scene's or film's path and a release name say of it: the studio, the title, the day
// or year it came out. The layouts are Stash's and Whisparr's: "[Studio] Title (2020-05-14).mp4" below a studio folder,
// and a scene group's "Studio.20.05.14.Performer.Name.Title.1080p".
package parseadult

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Info is what a name says. Date is yyyy-mm-dd when the name has a day, Year when it has only a year.
type Info struct {
	Studio string
	Title  string
	Date   string
	Year   int
	// Words are the name's words after the date in a release name, performers and title together: a release does not
	// say where one ends.
	Words string
	// Performer is the name a library file ends in after its date, when it has one.
	Performer string
	Group     string
	// Raw is the title as the name typed it when quality words were taken out of Title, for display; empty otherwise.
	Raw string
	// Season and Episode are the numbering a site gives its own scenes ("Title - S36E29"), kept out of Title because
	// providers carry the bare title; zero when the name has none.
	Season, Episode int
	// DurationSec is the file's own length when it was probed; it tells apart scenes that share a title, studio and day.
	DurationSec int
}

var (
	// libraryName is "[Studio] Title (2020-05-14)" or "Title (2018)", with the performer a multi-scene name ends in
	// ("... (2020-05-14) - Ada Vale"): the studio in brackets is optional and may be empty.
	libraryName = regexp.MustCompile(`^(?:\[([^\]]*)\]\s*)?(.*?)\s*\((\d{4})(?:-(\d{2})-(\d{2}))?\)(?:\s*-\s*(.+))?$`)
	// releaseName is "Studio.20.05.14.rest" with a two or four digit year.
	releaseName = regexp.MustCompile(`^([A-Za-z0-9]+)[. _-](\d{4}|\d{2})[. _-](\d{2})[. _-](\d{2})[. _-](.+)$`)
	// qualityStart ends the title words of a release name.
	qualityStart = regexp.MustCompile(`(?i)[. _-](?:2160p|1080p|720p|480p|360p|XXX|WEB(?:-?DL|RIP)?|HDRip|BluRay|MP4|MKV|H\.?26[45]|x26[45])\b`)
	// qualityWord is a resolution or format tag a person types into a library title; a provider's title has none of them.
	qualityWord = regexp.MustCompile(`(?i)\b(?:UHD|4K|8K|FHD|FullHD|HD|VR|60fps|2160p|1080p|720p)\b`)
	// siteNumbering is a trailing "- S36E29" (the dash optional) a studio's own numbering puts after a scene's title.
	siteNumbering = regexp.MustCompile(`(?i)\s+(?:-\s*)?S(\d{1,3})E(\d{1,4})$`)
	// bracketed is a (...) or [...] group of a title.
	bracketed   = regexp.MustCompile(`\(([^()]*)\)|\[([^\[\]]*)\]`)
	groupSuffix = regexp.MustCompile(`-([A-Za-z0-9]{2,12})$`)
	spaces      = regexp.MustCompile(`\s+`)
)

// ParsePath reads a library file's path below its root: the file's name, and the studio from the folder above the
// scene's own when the name has none.
func ParsePath(relPath string) (Info, bool) {
	segments := strings.Split(path.Clean(relPath), "/")
	base := segments[len(segments)-1]
	info, ok := ParseName(strings.TrimSuffix(base, path.Ext(base)))
	if !ok {
		return Info{}, false
	}
	// Studio/Title (date)/file: the studio is the folder two above the file.
	if info.Studio == "" && len(segments) >= 3 {
		info.Studio = strings.TrimSpace(segments[len(segments)-3])
	}
	return info, true
}

// ParseName reads a name without its extension, in either layout.
func ParseName(name string) (Info, bool) {
	name = strings.TrimSpace(name)
	if match := libraryName.FindStringSubmatch(name); match != nil {
		raw := tidy(match[2])
		title, season, episode := withoutSiteNumbering(withoutQualityWords(raw))
		info := Info{Studio: strings.TrimSpace(match[1]), Title: title, Season: season, Episode: episode, Performer: tidy(match[6])}
		if info.Title != raw {
			info.Raw = raw
		}
		info.Year, _ = strconv.Atoi(match[3])
		if match[4] != "" {
			info.Date = match[3] + "-" + match[4] + "-" + match[5]
		}
		return info, info.Title != ""
	}
	if info, ok := ParseRelease(name); ok {
		return info, true
	}
	title := tidy(name)
	return Info{Title: title}, title != ""
}

// ParseRelease reads a scene group's release name.
func ParseRelease(name string) (Info, bool) {
	name = strings.TrimSpace(strings.TrimSuffix(name, path.Ext(name)))
	match := releaseName.FindStringSubmatch(name)
	if match == nil {
		return Info{}, false
	}
	year, _ := strconv.Atoi(match[2])
	if len(match[2]) == 2 {
		year += 2000
		if year > 2040 {
			year -= 100
		}
	}
	info := Info{Studio: match[1], Year: year, Date: strconv.Itoa(year) + "-" + match[3] + "-" + match[4]}
	rest := match[5]
	if group := groupSuffix.FindStringSubmatch(rest); group != nil {
		info.Group = group[1]
		rest = strings.TrimSuffix(rest, group[0])
	}
	if cut := qualityStart.FindStringIndex(rest); cut != nil {
		rest = rest[:cut[0]]
	}
	info.Words = tidy(rest)
	info.Title = info.Words
	return info, info.Words != ""
}

// withoutSiteNumbering cuts a trailing site numbering off a title and answers it; a title that is only the numbering stays.
func withoutSiteNumbering(title string) (cleaned string, season, episode int) {
	found := siteNumbering.FindStringSubmatchIndex(title)
	if found == nil || strings.TrimSpace(title[:found[0]]) == "" {
		return title, 0, 0
	}
	season, _ = strconv.Atoi(title[found[2]:found[3]])
	episode, _ = strconv.Atoi(title[found[4]:found[5]])
	return strings.TrimSpace(title[:found[0]]), season, episode
}

// withoutQualityWords takes the quality tags out of a title: a bracket they empty goes, one that keeps other words
// ("(UHD #002)") is unwrapped; a title that is only a quality word stays as typed.
func withoutQualityWords(title string) string {
	if !qualityWord.MatchString(title) {
		return title
	}
	cleaned := bracketed.ReplaceAllStringFunc(title, func(group string) string {
		inner := group[1 : len(group)-1]
		if !qualityWord.MatchString(inner) {
			return group
		}
		return strings.TrimSpace(qualityWord.ReplaceAllString(inner, " "))
	})
	cleaned = spaces.ReplaceAllString(strings.TrimSpace(qualityWord.ReplaceAllString(cleaned, " ")), " ")
	if cleaned == "" {
		return title
	}
	return cleaned
}

// tidy turns separators into spaces and drops a trailing "- Scene 3"-style tail's spacing noise.
func tidy(text string) string {
	text = strings.NewReplacer("_", " ", ".", " ").Replace(text)
	return spaces.ReplaceAllString(strings.TrimSpace(text), " ")
}
