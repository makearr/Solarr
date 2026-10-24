// Package parsemovie turns a movie release or file name into title, year, edition and the ids it names, and reads what
// its encoding claims through parsetv's claim readers (the same video vocabulary as TV).
package parsemovie

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/identify/parsetv"
)

// Info is what a movie release or file name says about itself: identity separate from the technical Claims (ADR-18).
type Info struct {
	ReleaseTitle string
	// Title is the movie's name as the release writes it, with the year and everything after it removed.
	Title string
	// AllTitles holds the alternatives of "A / B" and "A AKA B" titles; empty otherwise.
	AllTitles []string
	// Year is the year the name gives, 0 when it gives none.
	Year int
	// Edition is a cut or edition the name states ("Director's Cut", "Extended"), as people write it; empty for the plain film.
	Edition string
	// IMDb and TMDB are ids the name carries ({imdb-tt0000001}, [tmdbid-603]).
	IMDb         string
	TMDB         int64
	ReleaseGroup string
	Claims       parsetv.Claims
}

var (
	imdbRe    = regexp.MustCompile(`(?i)[\[{(]\s*imdb(?:id)?[-= ]?(tt\d+)\s*[\]})]`)
	tmdbRe    = regexp.MustCompile(`(?i)[\[{(]\s*tmdb(?:id)?[-= ]?(\d+)\s*[\]})]`)
	editionRe = regexp.MustCompile(`(?i)[\[{(]\s*edition[-= ]([^\]})]+?)\s*[\]})]`)
	// tagRe takes out the square and curly tags left in a title ("[1080p]", "[YTS.MX]"); parentheses stay part of a
	// title ("Nusjbin or (The Izuxmatnov Tecmeu of Omnesaxho)") unless they hold only the technical half.
	tagRe   = regexp.MustCompile(`[\[{][^\]}]*[\]}]`)
	parenRe = regexp.MustCompile(`\([^)]*\)`)
	yearRe  = regexp.MustCompile(`(?:^|[\s.\[({_-])((?:19|20)\d{2})`)
	// qualityStart is where the technical half of a name begins; titles end before it.
	qualityStart = regexp.MustCompile(`(?i)(?:^|[\s.\[({_-])(?:2160p|1080p|1080i|720p|576p|480p|4k|uhd|bluray|blu-ray|bdrip|brrip|dvdrip|dvd|web[-. ]?dl|webrip|web[-. ](?:x26[45]|h[ .]?26[45]|av1|hevc|avc|ddp?5)|hdtv|hdrip|remux|x26[45]|h[ .]?26[45]|hevc|avc|xvid|divx|proper|repack|multi|truefrench|subbed|dubbed)(?:$|[\s.\])}_-])`)
	akaRe        = regexp.MustCompile(`(?i)\s+(?:aka|a\.k\.a\.?|/)\s+`)
	extensions   = map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".mov": true, ".wmv": true, ".ts": true, ".m2ts": true,
		".iso": true, ".mpg": true, ".mpeg": true, ".webm": true, ".flv": true, ".vob": true, ".nzb": true,
	}
	// editions are the words people give a cut, matched whole, longest first, and the name each gets.
	editions = []struct {
		re   *regexp.Regexp
		name string
	}{
		{regexp.MustCompile(`(?i)\bdirector'?s?[ ._-]?cut\b`), "Director's Cut"},
		{regexp.MustCompile(`(?i)\bfinal[ ._-]?cut\b`), "Final Cut"},
		{regexp.MustCompile(`(?i)\bextended(?:[ ._-]?(?:cut|edition|version))?\b`), "Extended"},
		{regexp.MustCompile(`(?i)\btheatrical(?:[ ._-]?(?:cut|edition|version))?\b`), "Theatrical"},
		{regexp.MustCompile(`(?i)\bunrated(?:[ ._-]?(?:cut|edition|version))?\b`), "Unrated"},
		{regexp.MustCompile(`(?i)\buncut\b`), "Uncut"},
		{regexp.MustCompile(`(?i)\bultimate(?:[ ._-]?(?:cut|edition))?\b`), "Ultimate"},
		{regexp.MustCompile(`(?i)\bspecial[ ._-]?edition\b`), "Special Edition"},
		{regexp.MustCompile(`(?i)\bcollector'?s?[ ._-]?edition\b`), "Collector's Edition"},
		{regexp.MustCompile(`(?i)\banniversary[ ._-]?edition\b`), "Anniversary Edition"},
		{regexp.MustCompile(`(?i)\bremastered\b`), "Remastered"},
		{regexp.MustCompile(`(?i)\bcriterion(?:[ ._-]?collection)?\b`), "Criterion"},
		{regexp.MustCompile(`(?i)\bimax\b`), "IMAX"},
		{regexp.MustCompile(`(?i)\bopen[ ._-]?matte\b`), "Open Matte"},
		{regexp.MustCompile(`(?i)\b3d\b`), "3D"},
	}
)

// Parse reads a movie release name or a file name (with or without its extension).
func Parse(name string) (Info, bool) {
	info := Info{ReleaseTitle: name}
	work := strings.TrimSpace(name)
	if extensions[strings.ToLower(path.Ext(work))] {
		work = strings.TrimSuffix(work, path.Ext(work))
	}
	work = readIDs(&info, work)
	work = readEdition(&info, work)
	info.Claims = parsetv.ParseClaims(work)
	info.ReleaseGroup = parsetv.ParseReleaseGroup(work)

	titlePart, year := splitTitle(work)
	info.Year = year
	title := cleanSeparators(titlePart)
	if title == "" {
		return info, false
	}
	info.Title, info.AllTitles = splitAlternatives(title)
	return info, true
}

// ParsePath reads a library or download path: the file name first; when it names no year the folder it is in
// ("Title (2001)/movie.mkv") supplies the title and year.
func ParsePath(rel string) (Info, bool) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	dir, file := path.Split(rel)
	info, ok := Parse(file)
	if ok && info.Year != 0 {
		return info, true
	}
	folder := path.Base(strings.TrimSuffix(dir, "/"))
	if folder == "" || folder == "." || folder == "/" {
		return info, ok
	}
	if fromFolder, folderOK := Parse(folder); folderOK && (fromFolder.Year != 0 || !ok) {
		fromFolder.ReleaseTitle = info.ReleaseTitle
		if info.Claims.HasQuality() || !fromFolder.Claims.HasQuality() {
			fromFolder.Claims = info.Claims
		}
		if info.ReleaseGroup != "" {
			fromFolder.ReleaseGroup = info.ReleaseGroup
		}
		if info.Edition != "" {
			fromFolder.Edition = info.Edition
		}
		return fromFolder, true
	}
	return info, ok
}

// ParseFolderOf reads the name of the folder a path sits in, for a file named differently from its folder: the folder is
// then the second opinion on which film it is.
func ParseFolderOf(rel string) (Info, bool) {
	dir, _ := path.Split(strings.ReplaceAll(rel, "\\", "/"))
	folder := path.Base(strings.TrimSuffix(dir, "/"))
	if folder == "" || folder == "." || folder == "/" {
		return Info{}, false
	}
	return Parse(folder)
}

func readIDs(info *Info, work string) string {
	if m := imdbRe.FindStringSubmatch(work); m != nil {
		info.IMDb = strings.ToLower(m[1])
		work = imdbRe.ReplaceAllString(work, " ")
	}
	if m := tmdbRe.FindStringSubmatch(work); m != nil {
		info.TMDB, _ = strconv.ParseInt(m[1], 10, 64)
		work = tmdbRe.ReplaceAllString(work, " ")
	}
	return work
}

// readEdition takes an explicit {edition-...} tag, else the first edition word of the name.
func readEdition(info *Info, work string) string {
	if m := editionRe.FindStringSubmatch(work); m != nil {
		info.Edition = strings.TrimSpace(m[1])
		return editionRe.ReplaceAllString(work, " ")
	}
	cut := qualityStartIndex(work)
	// With a year, the title is known: an edition word inside it ("Uncut Gems", "Final Cut") is the title's, not an edition.
	titleEnd := 0
	if title, year := splitTitle(work); year != 0 {
		titleEnd = strings.Index(work, title) + len(title)
	}
	for _, edition := range editions {
		for _, loc := range edition.re.FindAllStringIndex(work, -1) {
			if loc[0] >= titleEnd && (cut < 0 || loc[0] <= cut+1 || cut == 0) {
				info.Edition = edition.name
				return work
			}
		}
	}
	return work
}

func qualityStartIndex(work string) int {
	if loc := qualityStart.FindStringIndex(work); loc != nil {
		return loc[0]
	}
	return -1
}

// splitTitle cuts the name at its year, or where the technical half begins; the year is the last one before that
// point that has a title in front of it, so "2001 A Cfago Utenkoa 1968" and "Sample Runner 2049 (2017)" read right.
func splitTitle(work string) (title string, year int) {
	limit := len(work)
	if cut := qualityStartIndex(work); cut >= 0 {
		limit = cut
	}
	scan := work[:limit]
	matches := yearTokens(scan)
	for i := len(matches) - 1; i >= 0; i-- {
		start, end := matches[i][0], matches[i][1]
		if strings.TrimSpace(scan[:start]) == "" && i != 0 {
			continue
		}
		before := strings.Trim(scan[:start], " ._-([{")
		if before == "" {
			if len(matches) == 1 {
				// A name that is only a year ("2012", "1917") is that title.
				return scan[start:end], mustYear(scan[start:end])
			}
			continue
		}
		return before, mustYear(scan[start:end])
	}
	return strings.TrimSpace(scan), 0
}

// yearTokens are the spans of the four-digit years in text that stand alone, so "2049.2017" holds two.
func yearTokens(text string) [][2]int {
	var spans [][2]int
	for _, m := range yearRe.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[2], m[3]
		if end < len(text) && !strings.ContainsRune(" .])}_-", rune(text[end])) {
			continue
		}
		spans = append(spans, [2]int{start, end})
	}
	return spans
}

func mustYear(text string) int {
	year, _ := strconv.Atoi(text)
	return year
}

// cleanSeparators turns the dots and underscores a scene name uses for spaces into spaces, keeping a name that already
// has spaces as it is, and drops what is left of tags and a trailing dash.
func cleanSeparators(title string) string {
	title = tagRe.ReplaceAllStringFunc(title, func(tag string) string {
		if len(yearTokens(tag)) > 0 {
			return tag
		}
		return " "
	})
	title = parenRe.ReplaceAllStringFunc(title, func(tag string) string {
		if strings.TrimSpace(strings.Trim(tag, "()")) == "" || qualityStartIndex(tag) >= 0 && len(yearTokens(tag)) == 0 {
			return " "
		}
		return tag
	})
	if !strings.Contains(strings.TrimSpace(title), " ") {
		title = strings.NewReplacer(".", " ", "_", " ").Replace(title)
	} else {
		title = strings.ReplaceAll(title, "_", " ")
	}
	title = strings.Join(strings.Fields(title), " ")
	return strings.Trim(title, " -.")
}

// splitAlternatives reads "A / B" and "A AKA B" as one movie under several names, the first being the title; a
// parenthesised phrase stays in the title, since many titles have one.
func splitAlternatives(title string) (string, []string) {
	if parts := akaRe.Split(title, -1); len(parts) > 1 {
		return strings.TrimSpace(parts[0]), trimAll(parts)
	}
	return title, nil
}

func trimAll(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
