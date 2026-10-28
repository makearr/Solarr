// Package parsebook reads what a book or audiobook release name, or a library file's path, says: author, title, year,
// the formats it claims (EPUB, M4B), bitrate, abridgement and the ids some names carry (ISBN, ASIN). The patterns are
// Readarr's: "Author - Title (Year) [Format]", "Title by Author", and scene names with dots. Whether a release is a
// wanted book is decided by Names, Readarr's fuzzy search-criteria match, not by the parse alone.
package parsebook

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Info is what one name says.
type Info struct {
	Author string
	Title  string
	// Titles are every reading of the name's title, best first: "Series 03 - Title" offers "Title" and the whole.
	Titles []string
	Year   int
	// Formats are the file formats the name claims, lower case ("epub", "m4b"); a file's own extension comes first.
	Formats     []string
	BitrateKbps int
	Abridged    bool
	Unabridged  bool
	Retail      bool
	// Version is 2 and up for a PROPER, REPACK or v2.
	Version      int
	ReleaseGroup string
	ISBN, ASIN   string
}

// EbookFormats are the ebook file formats, Readarr's and Calibre's common ones.
var EbookFormats = []string{"epub", "epub3", "cbz", "azw3", "azw", "kfx", "mobi", "pdf", "cbr", "fb2", "djvu", "lit", "pdb", "pml", "rtf", "txt", "html", "htm", "docx"}

// AudioFormats are the audiobook file formats.
var AudioFormats = []string{"m4b", "m4a", "mp3", "aac", "flac", "ogg", "opus", "wma", "mp4", "wav"}

var (
	yearPattern      = regexp.MustCompile(`(?:^|[^0-9])((?:1[5-9]|20)[0-9]{2})(?:[^0-9]|$)`)
	bitratePattern   = regexp.MustCompile(`(?i)\b([0-9]{2,3})\s?k(?:bps|b/s|bit)?\b`)
	isbnPattern      = regexp.MustCompile(`\b(97[89][0-9]{10})\b`)
	asinPattern      = regexp.MustCompile(`\b(B0[0-9A-Z]{8})\b`)
	bracketPattern   = regexp.MustCompile(`[\[(\{][^\])\}]*[\])\}]`)
	versionPattern   = regexp.MustCompile(`(?i)\b(?:proper|repack|rerip)\b|\bv([2-9])\b`)
	groupPattern     = regexp.MustCompile(`-([A-Za-z0-9]{2,15})$`)
	numberingPattern = regexp.MustCompile(`(?i)^(?:(?:book|vol(?:ume)?|part|no)\.?\s*)?[0-9]{1,3}(?:\.[0-9])?\s*(?:[-.:)]\s*|\s+)`)
	separatorPattern = regexp.MustCompile(`\s+-\s+|\s*[–—]\s*`)
	byPattern        = regexp.MustCompile(`(?i)^(.+?)\s+by\s+(.+)$`)
	formatWord       = regexp.MustCompile(`(?i)\b(epub3|epub|azw3|azw|kfx|mobi|pdf|fb2|djvu|cbz|m4b|m4a|mp3|aac|flac|ogg|opus|wma)\b`)
	noiseWords       = regexp.MustCompile(`(?i)\b(?:unabridged|abridged|retail|ebook|e-book|audiobook|audio\s?book|proper|repack|rerip|` +
		`read\s+by\s+.+|narrated\s+by\s+.+|vbr|cbr|cbz|epub3|epub|azw3|azw|kfx|mobi|pdf|fb2|djvu|pdb|pml|m4b|m4a|mp3|aac|flac|ogg|opus|wma|` +
		`[0-9]{2,3}\s?k(?:bps|b/s)?)\b`)
)

// Parse reads a release name; ok is false when it names no title at all.
func Parse(name string) (Info, bool) {
	info := Info{}
	name = strings.TrimSpace(name)
	if extension := strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")); isFormat(extension) {
		info.Formats = append(info.Formats, extension)
		name = strings.TrimSuffix(name, path.Ext(name))
	}
	if !strings.Contains(name, " ") && strings.Count(name, ".")+strings.Count(name, "_") >= 2 {
		if group := groupPattern.FindStringSubmatch(name); group != nil {
			info.ReleaseGroup = group[1]
			name = strings.TrimSuffix(name, group[0])
		}
		name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
	}
	info.read(name)
	clean := bracketPattern.ReplaceAllString(name, " ")
	clean = noiseWords.ReplaceAllString(clean, " ")
	if info.Year > 0 {
		clean = strings.Replace(clean, strconv.Itoa(info.Year), " ", 1)
	}
	for _, id := range []string{info.ISBN, info.ASIN} {
		if id != "" {
			clean = strings.ReplaceAll(clean, id, " ")
		}
	}
	info.split(clean)
	return info, info.Title != ""
}

// read takes what the name claims beyond author and title.
func (info *Info) read(name string) {
	for _, word := range formatWord.FindAllString(name, -1) {
		if format := strings.ToLower(word); !slices.Contains(info.Formats, format) {
			info.Formats = append(info.Formats, format)
		}
	}
	if found := yearPattern.FindAllStringSubmatch(name, -1); len(found) > 0 {
		info.Year, _ = strconv.Atoi(found[len(found)-1][1])
	}
	if found := bitratePattern.FindStringSubmatch(name); found != nil {
		info.BitrateKbps, _ = strconv.Atoi(found[1])
	}
	if found := isbnPattern.FindStringSubmatch(name); found != nil {
		info.ISBN = found[1]
	}
	if found := asinPattern.FindStringSubmatch(name); found != nil {
		info.ASIN = found[1]
	}
	lowered := strings.ToLower(name)
	info.Unabridged = strings.Contains(lowered, "unabridged")
	info.Abridged = !info.Unabridged && strings.Contains(lowered, "abridged")
	info.Retail = strings.Contains(lowered, "retail")
	if found := versionPattern.FindStringSubmatch(name); found != nil {
		info.Version = 2
		if found[1] != "" {
			info.Version, _ = strconv.Atoi(found[1])
		}
	}
}

// split finds author and title in what is left: "Author - Title", "Author - Series 03 - Title", "Title by Author".
func (info *Info) split(clean string) {
	clean = collapse(clean)
	var parts []string
	for _, part := range separatorPattern.Split(clean, -1) {
		if part = strings.Trim(collapse(part), " -.,:;"); part != "" {
			parts = append(parts, part)
		}
	}
	switch {
	case len(parts) == 0:
		return
	case len(parts) == 1:
		if found := byPattern.FindStringSubmatch(parts[0]); found != nil {
			info.Title, info.Author = strings.TrimSpace(found[1]), strings.TrimSpace(found[2])
		} else {
			info.Title = parts[0]
		}
	default:
		info.Author = parts[0]
		info.Title = stripNumbering(parts[len(parts)-1])
		if len(parts) > 2 {
			info.Titles = append(info.Titles, strings.Join(parts[1:], " - "))
		}
		if found := byPattern.FindStringSubmatch(clean); found != nil && len(parts) == 2 && strings.Contains(parts[1], " by ") {
			info.Title, info.Author = strings.TrimSpace(found[1]), strings.TrimSpace(found[2])
		}
	}
	info.Title = stripNumbering(info.Title)
	info.Titles = append([]string{info.Title}, info.Titles...)
	if before, _, found := strings.Cut(info.Title, ":"); found && strings.TrimSpace(before) != "" {
		info.Titles = append(info.Titles, strings.TrimSpace(before))
	}
}

// ParsePath reads a library file's path. A book file names itself ("Author - Title.epub"); an audiobook's files are
// chapters, so byFolder reads the book from its folder and the author from the one above ("Author/Title (2001)/01.mp3").
func ParsePath(relPath string, byFolder bool) (Info, bool) {
	base := path.Base(relPath)
	extension := strings.ToLower(strings.TrimPrefix(path.Ext(base), "."))
	folder := path.Dir(relPath)
	var info Info
	var ok bool
	if byFolder && folder != "." {
		info, ok = Parse(path.Base(folder))
		folder = path.Dir(folder)
		if isFormat(extension) {
			info.Formats = append([]string{extension}, slices.DeleteFunc(info.Formats, func(format string) bool { return format == extension })...)
		}
	} else {
		info, ok = Parse(base)
	}
	if !ok {
		return info, false
	}
	if info.Author == "" && folder != "." {
		author := path.Base(folder)
		// "Author/Series/Title": a folder above a numbered book names the series, the one above that the author.
		if parent := path.Base(path.Dir(folder)); parent != "." && parent != "/" && numberingPattern.MatchString(info.Titles[0]+" ") {
			author = parent
		}
		if parsed, found := Parse(author); found {
			info.Author = parsed.Title
		}
	}
	return info, true
}

func isFormat(extension string) bool {
	return slices.Contains(EbookFormats, extension) || slices.Contains(AudioFormats, extension)
}

func stripNumbering(title string) string {
	stripped := strings.TrimSpace(numberingPattern.ReplaceAllString(title, ""))
	if stripped == "" {
		return strings.TrimSpace(title)
	}
	return stripped
}

func collapse(text string) string { return strings.Join(strings.Fields(text), " ") }

// CleanTitle is a title's words, lower case, without punctuation, accents kept, "&" as "and": what two spellings of one
// title share.
func CleanTitle(title string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(strings.NewReplacer("&", " and ", "'", "", "’", "").Replace(title)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		} else {
			out.WriteRune(' ')
		}
	}
	words := strings.Fields(out.String())
	if len(words) > 1 && slices.Contains([]string{"the", "a", "an"}, words[0]) {
		words = words[1:]
	}
	return strings.Join(words, " ")
}
