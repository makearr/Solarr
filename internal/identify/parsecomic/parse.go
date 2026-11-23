// Package parsecomic reads series, year, issue, volume, chapter, format, source and group from comic and manga file, folder and release names.
package parsecomic

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Claim is what one name says; it is a claim until a file's own metadata or probe confirms it.
type Claim struct {
	Series string
	Year   int
	// IssueNumber and IssueEnd are as printed minus leading zeros ("1", "1.5", "1AU", "-1", "0"); IssueEnd is set for a range.
	IssueNumber, IssueEnd string
	Volume, VolumeEnd     int
	// Chapter and ChapterEnd keep a decimal ("123.5").
	Chapter, ChapterEnd string
	// Format is tpb, hc, gn, omnibus, annual, one-shot or special.
	Format    string
	Publisher string
	// Source is digital, webrip, scan, c2c or hybrid.
	Source string
	Group  string
	// Edition is deluxe, fixed, repack, proper or remastered.
	Edition      string
	Language     string
	PageSizeHint int
	Extension    string
	// OfTotal is the "(of 6)" count.
	OfTotal int
	// Complete is set by a "Complete", "Complete Series" or "Collection" ending the title: every part of the series.
	Complete bool
}

var archiveExtensions = map[string]bool{"cbz": true, "cbr": true, "cb7": true, "cbt": true, "pdf": true, "epub": true, "zip": true, "rar": true, "7z": true, "tar": true}

var (
	bracketPattern  = regexp.MustCompile(`[\[(][^\])\[(]*[\])]`)
	yearBracket     = regexp.MustCompile(`^(?:19|20)\d{2}$`)
	ofPattern       = regexp.MustCompile(`(?i)^of\s?(\d{1,3})$`)
	pixelPattern    = regexp.MustCompile(`(?i)^(\d{3,5})\s?px$`)
	chapterPattern  = regexp.MustCompile(`(?i)(?:^|\s)(?:chapter|ch\.?|c)\s?(\d+(?:\.\d+)?)(?:\s?-\s?(?:chapter|ch\.?|c)?\s?(\d+(?:\.\d+)?))?(?:\s|$)`)
	volumePattern   = regexp.MustCompile(`(?i)(?:^|\s)(?:volume|vol\.?|v)\s?(\d{1,3})(?:\s?-\s?v?(\d{1,3}))?(?:\s|$)`)
	annualPattern   = regexp.MustCompile(`(?i)(?:^|\s)(annual|special)(?:\s+#?(\d{1,3}))?(?:\s|$)`)
	formatPattern   = regexp.MustCompile(`(?i)(?:^|\s)(tpb|hc|hardcover|gn|graphic novel|omnibus|one[- ]?shot)(?:\s|$)`)
	hashPattern     = regexp.MustCompile(`(?:^|\s)#\s?(-?\d+(?:\.\d+)?[A-Za-z]{0,3}|½)(?:\s?-\s?(\d+(?:\.\d+)?))?(?:\s|$)`)
	trailingPattern = regexp.MustCompile(`(?:^|\s)(-?\d+(?:\.\d+)?(?:\.?[A-Z]{1,3}|[a-z])?|½)(?:\s?-\s?(\d+(?:\.\d+)?))?\s*$`)
	completeTail    = regexp.MustCompile(`(?i)^(.*\S)\s+(?:complete(?:\s+(?:series|collection|set|run))?|collection)\s*$`)
	yearTail        = regexp.MustCompile(`^(.*\S)\s+((?:19|20)\d{2})\s*$`)
	decimalDot      = regexp.MustCompile(`^\d{1,2}(?:\D|$)`)
	markerDot       = regexp.MustCompile(`([A-Za-z0-9])\.([vVcC]\d)`)
)

var publishers = map[string]string{"marvel": "Marvel", "dc": "DC", "image": "Image", "dark horse": "Dark Horse", "idw": "IDW", "boom": "Boom", "viz": "Viz", "kodansha": "Kodansha", "yen press": "Yen Press", "seven seas": "Seven Seas", "dynamite": "Dynamite", "oni press": "Oni Press"}

var languages = map[string]string{"english": "en", "eng": "en", "en": "en", "japanese": "ja", "jp": "ja", "jpn": "ja", "french": "fr", "german": "de", "spanish": "es", "italian": "it", "korean": "ko", "chinese": "zh"}

// Parse reads a file name (with or without directories), a folder name or a release name.
func Parse(name string) Claim {
	var c Claim
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if dot := strings.LastIndex(base, "."); dot > 0 {
		if ext := strings.ToLower(base[dot+1:]); archiveExtensions[ext] {
			c.Extension = ext
			base = base[:dot]
		}
	}
	rest := c.takeBrackets(base)
	if !strings.Contains(rest, " ") {
		rest = dotsToSpaces(rest)
	}
	rest = markerDot.ReplaceAllString(rest, "$1 $2")
	rest = strings.Join(strings.Fields(strings.ReplaceAll(rest, "_", " ")), " ")
	rest = c.takeTitleMarkers(rest)
	c.Series = strings.Trim(rest, " -:–—")
	return c
}

// dotsToSpaces turns the dots of a dotted release name into spaces, keeping a decimal such as "1.5".
func dotsToSpaces(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		keep := s[i] != '.' || i > 0 && s[i-1] >= '0' && s[i-1] <= '9' && decimalDot.MatchString(s[i+1:])
		if keep {
			b.WriteByte(s[i])
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// takeBrackets classifies every bracketed group and returns the name without them.
func (c *Claim) takeBrackets(base string) string {
	var unclassified []string
	leading := ""
	for _, loc := range bracketPattern.FindAllStringIndex(base, -1) {
		inner := strings.TrimSpace(base[loc[0]+1 : loc[1]-1])
		if !c.classify(inner) && inner != "" {
			if loc[0] == 0 && base[0] == '[' {
				leading = inner
			} else {
				unclassified = append(unclassified, inner)
			}
		}
	}
	switch {
	case leading != "":
		c.Group = leading
	case len(unclassified) > 0:
		c.Group = unclassified[len(unclassified)-1]
	}
	return bracketPattern.ReplaceAllString(base, " ")
}

// classify records a recognised bracket word and reports whether it was one.
func (c *Claim) classify(inner string) bool {
	low := strings.ToLower(inner)
	switch {
	case yearBracket.MatchString(inner):
		c.Year, _ = strconv.Atoi(inner)
	case ofPattern.MatchString(inner):
		c.OfTotal, _ = strconv.Atoi(ofPattern.FindStringSubmatch(inner)[1])
	case pixelPattern.MatchString(inner):
		c.PageSizeHint, _ = strconv.Atoi(pixelPattern.FindStringSubmatch(inner)[1])
	case strings.HasPrefix(low, "digital"):
		c.Source = "digital"
	case low == "webrip" || low == "web-rip" || low == "web":
		c.Source = "webrip"
	case low == "scan" || low == "scans" || low == "scanned":
		c.Source = "scan"
	case low == "c2c" || low == "cover to cover":
		c.Source = "c2c"
	case low == "hybrid":
		c.Source = "hybrid"
	case low == "hd" || low == "sd" || low == "hq":
	case low == "deluxe" || low == "fixed" || low == "repack" || low == "proper" || low == "remastered":
		c.Edition = low
	case publishers[low] != "":
		c.Publisher = publishers[low]
	case languages[low] != "":
		c.Language = languages[low]
	default:
		return false
	}
	return true
}

// takeTitleMarkers pulls annual, format, volume, chapter and issue markers off the remaining text and returns the series text.
// Text after a volume or chapter marker is that volume's own title: only an issue number is read from it.
func (c *Claim) takeTitleMarkers(rest string) string {
	if m := annualPattern.FindStringSubmatch(rest); m != nil {
		c.Format = strings.ToLower(m[1])
		c.IssueNumber = trimZeros(m[2])
		rest = annualPattern.ReplaceAllString(rest, " ")
	}
	if m := formatPattern.FindStringSubmatch(rest); m != nil {
		c.Format = formatKey(m[1])
		rest = formatPattern.ReplaceAllString(rest, " ")
	}
	rest = strings.TrimSpace(rest)
	if m := completeTail.FindStringSubmatch(rest); m != nil {
		c.Complete = true
		rest = m[1]
	}
	title, after := rest, ""
	cut := len(rest)
	for _, re := range []*regexp.Regexp{chapterPattern, volumePattern} {
		if loc := re.FindStringIndex(rest); loc != nil && loc[0] < cut {
			cut = loc[0]
		}
	}
	if cut < len(rest) {
		title, after = strings.TrimSpace(rest[:cut]), rest[cut:]
		if m := chapterPattern.FindStringSubmatch(after); m != nil {
			c.Chapter, c.ChapterEnd = trimZeros(m[1]), trimZeros(m[2])
			after = chapterPattern.ReplaceAllString(after, " ")
		}
		if m := volumePattern.FindStringSubmatch(after); m != nil {
			c.Volume, _ = strconv.Atoi(m[1])
			c.VolumeEnd, _ = strconv.Atoi(m[2])
			after = volumePattern.ReplaceAllString(after, " ")
		}
		after = strings.TrimSpace(after)
		if c.IssueNumber == "" {
			c.takeIssue(&after, true)
		}
	} else if c.IssueNumber == "" {
		c.takeIssue(&title, false)
	}
	if m := yearTail.FindStringSubmatch(title); m != nil && c.Year == 0 {
		c.Year, _ = strconv.Atoi(m[2])
		title = m[1]
		if c.IssueNumber == "" && cut == len(rest) {
			c.takeIssue(&title, false)
		}
	}
	return title
}

// takeIssue reads a "#12" or a trailing bare number and removes it from text.
func (c *Claim) takeIssue(text *string, alone bool) {
	if m := hashPattern.FindStringSubmatch(*text); m != nil {
		c.IssueNumber, c.IssueEnd = trimZeros(m[1]), trimZeros(m[2])
		*text = strings.TrimSpace(hashPattern.ReplaceAllString(*text, " "))
	} else if m := trailingPattern.FindStringSubmatch(*text); m != nil && c.takeTrailingNumber(*text, m, alone) {
		*text = strings.TrimSpace(trailingPattern.ReplaceAllString(*text, " "))
	}
}

// takeTrailingNumber accepts a bare trailing number unless it is the only word of a title or a lone year.
func (c *Claim) takeTrailingNumber(rest string, m []string, alone bool) bool {
	if !alone && strings.TrimSpace(rest) == strings.TrimSpace(m[0]) {
		return false
	}
	if yearBracket.MatchString(m[1]) && m[2] == "" && c.Year == 0 {
		return false
	}
	c.IssueNumber, c.IssueEnd = trimZeros(m[1]), trimZeros(m[2])
	return true
}

func formatKey(word string) string {
	switch strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(word)) {
	case "hardcover":
		return "hc"
	case "graphicnovel":
		return "gn"
	case "oneshot":
		return "one-shot"
	default:
		return strings.ToLower(word)
	}
}

// trimZeros drops leading zeros of the integer part: "007" is "7", "000" is "0", "-01" is "-1", "007.5" is "7.5".
func trimZeros(s string) string {
	if s == "½" {
		return "0.5"
	}
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	i := 0
	for i < len(s)-1 && s[i] == '0' && s[i+1] >= '0' && s[i+1] <= '9' {
		i++
	}
	return sign + s[i:]
}
