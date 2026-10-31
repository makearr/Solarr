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
}

var (
	// libraryName is "[Studio] Title (2020-05-14)" or "Title (2018)", with the performer a multi-scene name ends in
	// ("... (2020-05-14) - Ada Vale"): the studio in brackets is optional and may be empty.
	libraryName = regexp.MustCompile(`^(?:\[([^\]]*)\]\s*)?(.*?)\s*\((\d{4})(?:-(\d{2})-(\d{2}))?\)(?:\s*-\s*(.+))?$`)
	// releaseName is "Studio.20.05.14.rest" with a two or four digit year.
	releaseName = regexp.MustCompile(`^([A-Za-z0-9]+)[. _-](\d{4}|\d{2})[. _-](\d{2})[. _-](\d{2})[. _-](.+)$`)
	// qualityStart ends the title words of a release name.
	qualityStart = regexp.MustCompile(`(?i)[. _-](?:2160p|1080p|720p|480p|360p|XXX|WEB(?:-?DL|RIP)?|HDRip|BluRay|MP4|MKV|H\.?26[45]|x26[45])\b`)
	groupSuffix  = regexp.MustCompile(`-([A-Za-z0-9]{2,12})$`)
	spaces       = regexp.MustCompile(`\s+`)
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
		info := Info{Studio: strings.TrimSpace(match[1]), Title: tidy(match[2]), Performer: tidy(match[6])}
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

// tidy turns separators into spaces and drops a trailing "- Scene 3"-style tail's spacing noise.
func tidy(text string) string {
	text = strings.NewReplacer("_", " ", ".", " ").Replace(text)
	return spaces.ReplaceAllString(strings.TrimSpace(text), " ")
}
