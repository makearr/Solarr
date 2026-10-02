package parsetv

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dlclark/regexp2"
	"golang.org/x/text/unicode/norm"
)

const matchTimeout = 2 * time.Second

func compile(pattern string, options regexp2.RegexOptions) *regexp2.Regexp {
	re := regexp2.MustCompile(pattern, options)
	re.MatchTimeout = matchTimeout
	return re
}

var (
	reportTitleRes = compileAll(reportTitlePatterns)
	preSubRes      = compileSubstitutions(preSubstitutions)

	specialEpisodeTitleRes = []*regexp2.Regexp{
		compile(`\.S\d+E00\.(?<episodetitle>.+?)(?:\.(?:720p|1080p|2160p|HDTV|WEB|WEBRip|WEB-DL)\.|$)`, regexp2.IgnoreCase),
		compile(`\.S\d+\.Special\.(?<episodetitle>.+?)(?:\.(?:720p|1080p|2160p|HDTV|WEB|WEBRip|WEB-DL)\.|$)`, regexp2.IgnoreCase),
	}

	rejectHashedRes = compileAllOptions(0,
		`^[0-9a-zA-Z]{32}`, `^[a-z0-9]{24}$`, `^[A-Z]{11}\d{3}$`, `^[a-z]{12}\d{3}$`, `^Backup_\d{5,}S\d{2}-\d{2}$`,
		`^123$`, `^\d{6}_\d{2}$`, `^[0-9a-zA-Z]{30}`, `^[0-9a-zA-Z]{26}`, `^[0-9a-zA-Z]{39}`, `^[0-9a-zA-Z]{24}`)
	rejectHashedInsensitiveRes = compileAllOptions(regexp2.IgnoreCase, `^abc$`, `^abc[-_. ]xyz`, `^b00bs$`)

	seasonFolderReject  = compile(`^(Season[ ._-]*\d+|Specials)$`, 0)
	reversedTitleRe     = compile(`(?:^|[-._ ])(p027|p0801|\d{2,3}E\d{2}S)[-._ ]`, 0)
	normalizeRe         = compile(`((?:\b|_)(?<!^)([aà](?!$)|an|the|and|or|of)(?!$)(?:\b|_))|\W|_`, regexp2.IgnoreCase)
	percentRe           = compile(`(?<=\b\d+)%`, 0)
	fileExtensionRe     = compile(`\.[a-z0-9]{2,4}$`, regexp2.IgnoreCase)
	simpleTitleRe       = compile(`(?:(480|540|576|720|1080|2160)[ip]|[xh][\W_]?26[45]|DD\W?5\W1|[<>?*]|848x480|1280x720|1920x1080|3840x2160|4096x2160|(?<![a-f0-9])(8|10)(b(?![a-z0-9])|bit)|10-bit)\s*?`, regexp2.IgnoreCase)
	websitePrefixRe     = compile(`^(?:(?:\[|\()\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?<!Risube-Kun\.)(?:[a-z]{2,6}\.[a-z]{2,6}|xn--[a-z0-9-]{4,}|[a-z]{2,})\b(?:\s*(?:\]|\))|[ -]{2,})[ -]*`, regexp2.IgnoreCase)
	websitePostfixRe    = compile(`(?:\[\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?:xn--[a-z0-9-]{4,}|[a-z]{2,6})\b(?:\s*\])$`, regexp2.IgnoreCase)
	sixDigitAirDateRe   = compile(`(?<=[_.-])(?<airdate>(?<!\d)(?<airyear>[1-9]\d{1})(?<airmonth>[0-1][0-9])(?<airday>[0-3][0-9]))(?=[_.-])`, regexp2.IgnoreCase)
	cleanGroupRe        = compile(`^(.*?[-._ ](S\d+E\d+)[-._ ])|(-(RP|1|NZBGeek|Obfuscated|Scrambled|sample|Pre|postbot|xpost|Rakuv[a-z0-9]*|WhiteRev|BUYMORE|AsRequested|AlternativeToRequested|GEROV|Z0iDS3N|Chamele0n|4P|4Planet|AlteZachen|RePACKPOST))+$`, regexp2.IgnoreCase)
	cleanTorrentSuffix  = compile(`\[(?:ettv|rartv|rarbg|cttv|publichd)\]$`, regexp2.IgnoreCase)
	cleanQualityBracket = compile(`\[[a-z0-9 ._-]+\]$`, regexp2.IgnoreCase)
	releaseGroupRe      = compile(`-(?<releasegroup>[a-z0-9]+(?<part2>-[a-z0-9]+)?(?!.+?(?:480p|576p|720p|1080p|2160p)))(?<!(?:WEB-DL|Blu-Ray|480p|576p|720p|1080p|2160p|DTS-HD|DTS-X|DTS-MA|DTS-ES|-ES|-EN|-CAT|[ ._]\d{4}-\d{2}|-\d{2})(?:\k<part2>)?)(?:\b|[-._ ]|$)|[-._ ]\[(?<releasegroup>[a-z0-9]+)\]$`, regexp2.IgnoreCase)
	invalidGroupRe      = compile(`^([se]\d+|[0-9a-f]{8})$`, regexp2.IgnoreCase)
	animeGroupRe        = compile(`^(?:\[(?<subgroup>(?!\s).+?(?<!\s))\](?:_|-|\s|\.)?)`, regexp2.IgnoreCase)
	exceptionExactRe    = compile(`(?<releasegroup>(?:D\-Z0N3|Fight-BB|VARYG|E\.N\.D|KRaLiMaRKo|BluDragon|DarQ|KCRT|BEN[_. ]THE[_. ]MEN)\b)`, regexp2.IgnoreCase)
	exceptionGroupRe    = compile(`(?<=[._ \[])(?<releasegroup>(Silence|afm72|Panda|Ghost|MONOLITH|Tigole|Joy|ImE|UTR|t3nzin|Anime Time|Project Angel|Hakata Ramen|HONE|Vyndros|SEV|Garshasp|Kappa|Natty|RCVR|SAMPA|YOGI|r00t|EDGE2020|RZeroX)(?=\]|\)))`, regexp2.IgnoreCase)
	yearInTitleRe       = compile(`^(?<title>.+?)[-_. ]+?[\(\[]?(?<year>\d{4})[\]\)]?`, regexp2.IgnoreCase)
	titleComponentsRe   = compile(`^(?:(?<title>.+?) \((?<title>.+?)\)|(?<title>.+?) \| (?<title>.+?)|(?<title>.+?) AKA (?<title>.+?))$`, regexp2.IgnoreCase)
	partRe              = compile(`\(\d+\)$`, 0)
	punctuationRe       = compile(`[^\w\s]`, 0)
	articleWordRe       = compile(`^(a|an|the)\s`, regexp2.IgnoreCase)
	duplicateSpacesRe   = compile(`\s{2,}`, 0)
	seasonFolderRe      = compile(`^(?:S|Season|Saison|Series|Stagione)[-_. ]*(?<season>(?<!\d+)\d{1,4}(?!\d+))(?:[_. ]+(?!\d+)|$)`, regexp2.IgnoreCase)
	simpleEpisodeNumber = compile(`^[ex]?(?<episode>(?<!\d+)\d{1,3}(?!\d+))(?:[ex-](?<episode>(?<!\d+)\d{1,3}(?!\d+)))?(?:[_. ](?!\d+)(?<remaining>.+)|$)`, regexp2.IgnoreCase)
	requestInfoRe       = compile(`^(?:\[.+?\])+`, 0)
)

var errInvalidDate = errors.New("invalid air date")

var numberWords = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}

var mediaExtensions = map[string]bool{
	".webm": true, ".m4v": true, ".3gp": true, ".nsv": true, ".ty": true, ".strm": true, ".rm": true, ".rmvb": true,
	".m3u": true, ".ifo": true, ".mov": true, ".qt": true, ".divx": true, ".xvid": true, ".bivx": true, ".nrg": true,
	".pva": true, ".wmv": true, ".asf": true, ".asx": true, ".ogm": true, ".ogv": true, ".m2v": true, ".avi": true,
	".bin": true, ".dat": true, ".dvr-ms": true, ".mpg": true, ".mpeg": true, ".mp4": true, ".avc": true, ".vp3": true,
	".svq3": true, ".nuv": true, ".viv": true, ".dv": true, ".fli": true, ".flv": true, ".wpl": true, ".img": true,
	".iso": true, ".vob": true, ".mkv": true, ".mk3d": true, ".ts": true, ".wtv": true, ".m2ts": true,
	".par2": true, ".nzb": true,
}

func compileAll(patterns []string) []*regexp2.Regexp {
	return compileAllOptions(regexp2.IgnoreCase, patterns...)
}

func compileAllOptions(options regexp2.RegexOptions, patterns ...string) []*regexp2.Regexp {
	compiled := make([]*regexp2.Regexp, len(patterns))
	for i, pattern := range patterns {
		compiled[i] = compile(pattern, options)
	}
	return compiled
}

type compiledSubstitution struct {
	re          *regexp2.Regexp
	replacement string
}

func compileSubstitutions(subs []substitution) []compiledSubstitution {
	compiled := make([]compiledSubstitution, len(subs))
	for i, sub := range subs {
		compiled[i] = compiledSubstitution{compile(sub.pattern, 0), sub.replacement}
	}
	return compiled
}

func isMatch(re *regexp2.Regexp, s string) bool {
	ok, err := re.MatchString(s)
	return err == nil && ok
}

func replace(re *regexp2.Regexp, s, with string) string {
	out, err := re.Replace(s, with, -1, -1)
	if err != nil {
		return s
	}
	return out
}

func replaceFunc(re *regexp2.Regexp, s string, with func(regexp2.Match) string) string {
	out, err := re.ReplaceFunc(s, with, -1, -1)
	if err != nil {
		return s
	}
	return out
}

func firstMatch(re *regexp2.Regexp, s string) *regexp2.Match {
	m, err := re.FindStringMatch(s)
	if err != nil {
		return nil
	}
	return m
}

func allMatches(re *regexp2.Regexp, s string) []*regexp2.Match {
	var matches []*regexp2.Match
	m := firstMatch(re, s)
	for m != nil {
		matches = append(matches, m)
		next, err := re.FindNextMatch(m)
		if err != nil {
			break
		}
		m = next
	}
	return matches
}

func captures(m *regexp2.Match, name string) []regexp2.Capture {
	group := m.GroupByName(name)
	if group == nil {
		return nil
	}
	return group.Captures
}

func groupValue(m *regexp2.Match, name string) (string, bool) {
	caps := captures(m, name)
	if len(caps) == 0 {
		return "", false
	}
	return caps[len(caps)-1].String(), true
}

func endIndex(c regexp2.Capture) int { return c.Index + c.Length }

func groupEnd(m *regexp2.Match, name string) int {
	caps := captures(m, name)
	if len(caps) == 0 {
		return 0
	}
	return endIndex(caps[len(caps)-1])
}

// Parse reads a release or file name; ok is false when nothing in it is recognisable as an episode.
func Parse(title string, now time.Time) (Info, bool) {
	if !validBeforeParsing(title) {
		return Info{}, false
	}
	if isMatch(reversedTitleRe, title) {
		title = reverseTitle(title)
	}
	releaseTitle := removeFileExtension(title)
	releaseTitle = strings.NewReplacer("【", "[", "】", "]").Replace(releaseTitle)
	for _, sub := range preSubRes {
		if isMatch(sub.re, releaseTitle) {
			releaseTitle = replace(sub.re, releaseTitle, sub.replacement)
		}
	}

	simple := replace(simpleTitleRe, releaseTitle, "")
	simple = replace(websitePrefixRe, simple, "")
	simple = replace(websitePostfixRe, simple, "")
	simple = replace(cleanTorrentSuffix, simple, "")
	simple = replaceFunc(cleanQualityBracket, simple, func(m regexp2.Match) string {
		if ParseClaims(m.String()).HasQuality() {
			return ""
		}
		return m.String()
	})
	if m := firstMatch(sixDigitAirDateRe, simple); m != nil {
		year, _ := groupValue(m, "airyear")
		month, _ := groupValue(m, "airmonth")
		day, _ := groupValue(m, "airday")
		if month != "00" || day != "00" {
			date, _ := groupValue(m, "airdate")
			simple = strings.Replace(simple, date, "20"+year+"."+month+"."+day, 1)
		}
	}

	for _, re := range reportTitleRes {
		matches := allMatches(re, simple)
		if len(matches) == 0 {
			continue
		}
		info, ok, err := parseMatches(matches, releaseTitle, now)
		if errors.Is(err, errInvalidDate) {
			break
		}
		if !ok {
			continue
		}
		if info.FullSeason && containsFold(info.ReleaseTokens, "Special") {
			info.FullSeason = false
			info.Special = true
		}
		info.Claims = ParseClaims(title)
		info.Claims.Languages = ParseLanguages(info.ReleaseTokens)
		info.ReleaseGroup = ParseReleaseGroup(releaseTitle)
		if sub, found := groupValue(matches[0], "subgroup"); found && strings.TrimSpace(sub) != "" {
			info.ReleaseGroup = sub
		}
		info.ReleaseHash = releaseHash(matches[0])
		return info, true
	}
	return Info{}, false
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func reverseTitle(title string) string {
	withoutExtension := []rune(removeFileExtension(title))
	for i, j := 0, len(withoutExtension)-1; i < j; i, j = i+1, j-1 {
		withoutExtension[i], withoutExtension[j] = withoutExtension[j], withoutExtension[i]
	}
	return string(withoutExtension) + string([]rune(title)[len(withoutExtension):])
}

func validBeforeParsing(title string) bool {
	lower := strings.ToLower(title)
	if strings.Contains(lower, "password") && strings.Contains(lower, "yenc") {
		return false
	}
	if !strings.ContainsFunc(title, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
		return false
	}
	withoutExtension := removeFileExtension(title)
	for _, re := range rejectHashedRes {
		if isMatch(re, withoutExtension) {
			return false
		}
	}
	for _, re := range rejectHashedInsensitiveRes {
		if isMatch(re, withoutExtension) {
			return false
		}
	}
	return !isMatch(seasonFolderReject, withoutExtension)
}

func removeFileExtension(title string) string {
	return replaceFunc(fileExtensionRe, title, func(m regexp2.Match) string {
		if mediaExtensions[strings.ToLower(m.String())] {
			return ""
		}
		return m.String()
	})
}

func releaseHash(m *regexp2.Match) string {
	value, found := groupValue(m, "hash")
	if !found {
		return ""
	}
	value = strings.Trim(value, "[]()")
	if value == "1280x720" {
		return ""
	}
	return value
}

func parseNumber(value string) (int, error) {
	normalized := toNumerals(norm.NFKC.String(value))
	if n, err := strconv.Atoi(normalized); err == nil {
		return n, nil
	}
	lower := strings.ToLower(value)
	for i, word := range numberWords {
		if word == lower {
			return i, nil
		}
	}
	return 0, errors.New("not a number: " + value)
}

func parseDecimal(value string) (float64, error) {
	return strconv.ParseFloat(toNumerals(norm.NFKC.String(value)), 64)
}

// toNumerals maps any Unicode decimal digit to its ASCII digit, as .NET's GetNumericValue does.
func toNumerals(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsDigit(r):
			b.WriteRune('0' + digitValue(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func digitValue(r rune) rune {
	for _, table := range unicode.Nd.R16 {
		if uint16(r) >= table.Lo && uint16(r) <= table.Hi && r <= 0xFFFF {
			return (r - rune(table.Lo)) % 10
		}
	}
	for _, table := range unicode.Nd.R32 {
		if uint32(r) >= table.Lo && uint32(r) <= table.Hi {
			return (r - rune(table.Lo)) % 10
		}
	}
	return 0
}

func parseMatches(matches []*regexp2.Match, releaseTitle string, now time.Time) (Info, bool, error) {
	first := matches[0]
	title, _ := groupValue(first, "title")
	seriesName := strings.NewReplacer(".", " ", "_", " ").Replace(title)
	seriesName = strings.Trim(replace(requestInfoRe, seriesName, ""), " ")

	airYearText, _ := groupValue(first, "airyear")
	airYear, _ := strconv.Atoi(airYearText)
	lastIndex := groupEnd(first, "title")

	var info Info
	if airYear < 1900 {
		info = Info{ReleaseTitle: releaseTitle}
		for _, m := range matches {
			if ok := applyNumbering(&info, m, first, &lastIndex); !ok {
				return Info{}, false, nil
			}
		}
		applySeasons(&info, first, &lastIndex)
	} else {
		daily, err := parseDaily(first, airYear, now)
		if err != nil {
			return Info{}, false, err
		}
		info = daily
		info.ReleaseTitle = releaseTitle
		for _, name := range []string{"airyear", "airmonth", "airday"} {
			lastIndex = max(lastIndex, groupEnd(first, name))
		}
	}

	runes := []rune(releaseTitle)
	if lastIndex < len(runes) {
		info.ReleaseTokens = string(runes[lastIndex:])
	} else {
		info.ReleaseTokens = releaseTitle
	}
	info.SeriesTitle = seriesName
	fillTitleInfo(&info, first)
	return info, true, nil
}

func applyNumbering(info *Info, m, first *regexp2.Match, lastIndex *int) bool {
	episodeCaps := captures(m, "episode")
	absoluteCaps := captures(m, "absoluteepisode")
	_, special := groupValue(m, "special")

	if len(episodeCaps) > 0 {
		low, err1 := parseNumber(episodeCaps[0].String())
		high, err2 := parseNumber(episodeCaps[len(episodeCaps)-1].String())
		if err1 != nil || err2 != nil || low > high {
			return false
		}
		info.EpisodeNumbers = info.EpisodeNumbers[:0:0]
		for n := low; n <= high; n++ {
			info.EpisodeNumbers = append(info.EpisodeNumbers, n)
		}
		*lastIndex = max(*lastIndex, endIndex(episodeCaps[len(episodeCaps)-1]))
		if special {
			info.Special = true
		}
		if _, split := groupValue(m, "splitepisode"); split {
			info.SplitEpisode = true
		}
	}

	if len(absoluteCaps) > 0 {
		low, err1 := parseDecimal(absoluteCaps[0].String())
		high, err2 := parseDecimal(absoluteCaps[len(absoluteCaps)-1].String())
		if err1 != nil || err2 != nil || low > high {
			return false
		}
		if low != float64(int(low)) || high != float64(int(high)) {
			if len(absoluteCaps) != 1 {
				return false
			}
			info.SpecialAbsolutes = []float64{low}
			info.Special = true
			*lastIndex = max(*lastIndex, endIndex(absoluteCaps[0]))
		} else {
			info.AbsoluteNumbers = info.AbsoluteNumbers[:0:0]
			for n := int(low); n <= int(high); n++ {
				info.AbsoluteNumbers = append(info.AbsoluteNumbers, n)
			}
			if special {
				info.Special = true
			}
			*lastIndex = max(*lastIndex, endIndex(absoluteCaps[len(absoluteCaps)-1]))
		}
	}

	if len(episodeCaps) == 0 && len(absoluteCaps) == 0 {
		if extras, _ := groupValue(first, "extras"); strings.TrimSpace(extras) != "" {
			info.SeasonExtra = true
		}
		if part, _ := groupValue(first, "seasonpart"); strings.TrimSpace(part) != "" {
			info.SeasonPart, _ = strconv.Atoi(part)
			info.PartialSeason = true
		} else {
			info.FullSeason = true
		}
	}

	if count, found := groupValue(first, "episodecount"); found && len(episodeCaps) == 2 && episodeCaps[1].String() == count {
		info.EpisodeNumbers = nil
		info.FullSeason = true
	}
	return true
}

func applySeasons(info *Info, first *regexp2.Match, lastIndex *int) {
	distinct := map[int]bool{}
	for _, c := range captures(first, "season") {
		n, err := strconv.Atoi(c.String())
		if err != nil {
			continue
		}
		info.Seasons = append(info.Seasons, n)
		distinct[n] = true
		*lastIndex = max(*lastIndex, endIndex(c))
	}
	if len(distinct) > 1 {
		info.MultiSeason = true
	}
	if len(info.Seasons) > 0 {
		info.SeasonNumber = info.Seasons[0]
	} else if len(info.AbsoluteNumbers) == 0 && len(info.EpisodeNumbers) > 0 {
		info.SeasonNumber = 1
		info.MiniSeries = true
	}
}

func parseDaily(m *regexp2.Match, year int, now time.Time) (Info, error) {
	var month, day int
	ambiguousMonth, hasMonth := groupValue(m, "ambiguousairmonth")
	ambiguousDay, hasDay := groupValue(m, "ambiguousairday")
	if hasMonth && hasDay {
		month, _ = strconv.Atoi(ambiguousMonth)
		day, _ = strconv.Atoi(ambiguousDay)
		if day <= 12 && month <= 12 {
			return Info{}, errInvalidDate
		}
	} else {
		monthText, _ := groupValue(m, "airmonth")
		dayText, _ := groupValue(m, "airday")
		month, _ = strconv.Atoi(monthText)
		day, _ = strconv.Atoi(dayText)
		if month > 12 {
			month, day = day, month
		}
	}

	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if date.Year() != year || int(date.Month()) != month || date.Day() != day {
		return Info{}, errInvalidDate
	}
	tomorrow := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	if date.After(tomorrow) {
		return Info{}, errInvalidDate
	}
	if titleYear, _ := groupValue(m, "titleyear"); date.Before(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)) && strings.TrimSpace(titleYear) == "" {
		return Info{}, errInvalidDate
	}

	info := Info{AirDate: date.Format("2006-01-02")}
	if part, found := groupValue(m, "part"); found {
		info.DailyPart, _ = strconv.Atoi(part)
	}
	return info, nil
}

func fillTitleInfo(info *Info, first *regexp2.Match) {
	info.TitleWithoutYear = info.SeriesTitle
	if m := firstMatch(yearInTitleRe, info.SeriesTitle); m != nil {
		info.TitleWithoutYear, _ = groupValue(m, "title")
		year, _ := groupValue(m, "year")
		info.Year, _ = strconv.Atoi(year)
	}
	if m := firstMatch(titleComponentsRe, info.TitleWithoutYear); m != nil {
		for _, c := range captures(m, "title") {
			info.AllTitles = append(info.AllTitles, c.String())
		}
	} else if caps := captures(first, "title"); len(caps) > 1 {
		for _, c := range caps {
			info.AllTitles = append(info.AllTitles, strings.NewReplacer(".", " ", "_", " ").Replace(c.String()))
		}
	}
}

// ParseReleaseGroup returns the scene or anime sub-group a name credits, or "".
func ParseReleaseGroup(title string) string {
	title = removeFileExtension(strings.TrimSpace(title))
	for _, sub := range preSubRes {
		if isMatch(sub.re, title) {
			title = replace(sub.re, title, sub.replacement)
			break
		}
	}
	title = replace(websitePrefixRe, title, "")
	title = replace(cleanTorrentSuffix, title, "")

	if m := firstMatch(animeGroupRe, title); m != nil {
		value, _ := groupValue(m, "subgroup")
		return value
	}
	title = replace(cleanGroupRe, title, "")

	for _, re := range []*regexp2.Regexp{exceptionGroupRe, exceptionExactRe} {
		if matches := allMatches(re, title); len(matches) > 0 {
			value, _ := groupValue(matches[len(matches)-1], "releasegroup")
			return value
		}
	}
	matches := allMatches(releaseGroupRe, title)
	if len(matches) == 0 {
		return ""
	}
	group, _ := groupValue(matches[len(matches)-1], "releasegroup")
	if _, err := strconv.Atoi(group); err == nil || isMatch(invalidGroupRe, group) {
		return ""
	}
	return group
}

// CleanTitle reduces a series title to the form series are matched by.
func CleanTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return title
	}
	if _, err := strconv.ParseInt(title, 10, 64); err == nil {
		return title
	}
	title = replace(percentRe, title, "percent")
	return removeAccents(strings.ToLower(replace(normalizeRe, title, "")))
}

func removeAccents(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizeEpisodeTitle lowers an episode title to the form episode titles are compared in.
func NormalizeEpisodeTitle(title string) string {
	for _, re := range specialEpisodeTitleRes {
		if m := firstMatch(re, title); m != nil {
			title, _ = groupValue(m, "episodetitle")
			break
		}
	}
	title = replace(partRe, title, "")
	title = replace(punctuationRe, title, " ")
	title = replace(duplicateSpacesRe, title, " ")
	return strings.ToLower(strings.TrimSpace(title))
}

// NormalizeTitle lowers a title for loose comparison: no punctuation, no leading article.
func NormalizeTitle(title string) string {
	title = replace(punctuationRe, title, "")
	title = replace(articleWordRe, title, "")
	title = replace(duplicateSpacesRe, title, " ")
	return strings.ToLower(strings.TrimSpace(title))
}

// ParsePath reads a file path: the file name first, then season folder plus file, then the folder name.
func ParsePath(path string, now time.Time) (Info, bool) {
	dir, file := splitPath(path)
	info, ok := Parse(file, now)

	if numbers := firstMatch(simpleEpisodeNumber, file); numbers != nil && dir != "" && (!ok || info.MiniSeries || len(info.AbsoluteNumbers) > 0) {
		if season := firstMatch(seasonFolderRe, dir); season != nil {
			if seasonNumber, found := groupValue(season, "season"); found {
				caps := captures(numbers, "episode")
				low, _ := parseNumber(caps[0].String())
				high, _ := parseNumber(caps[len(caps)-1].String())
				synthetic := "S" + seasonNumber + "E" + twoDigits(low)
				if low != high {
					synthetic += "-E" + twoDigits(high)
				}
				if remaining, found := groupValue(numbers, "remaining"); found {
					synthetic += " " + remaining
				}
				return Parse(synthetic, now)
			}
		}
	}

	if !ok {
		stem := strings.TrimSuffix(file, extensionOf(file))
		if number, err := strconv.Atoi(stem); err == nil {
			folder, folderOK := Parse(dir, now)
			switch {
			case folderOK && containsInt(folder.AbsoluteNumbers, number):
				folder.AbsoluteNumbers = []int{number}
				return folder, true
			case folderOK && containsInt(folder.EpisodeNumbers, number):
				folder.EpisodeNumbers = []int{number}
				return folder, true
			}
		}
	}
	if !ok {
		info, ok = Parse(dir+" "+file, now)
	}
	if !ok {
		info, ok = Parse(dir+extensionOf(file), now)
	}
	return info, ok
}

func splitPath(path string) (dir, file string) {
	path = strings.ReplaceAll(path, `\`, "/")
	cut := strings.LastIndex(path, "/")
	file = path[cut+1:]
	if cut < 0 {
		return "", file
	}
	dirPath := path[:cut]
	return dirPath[strings.LastIndex(dirPath, "/")+1:], file
}

func extensionOf(file string) string {
	if i := strings.LastIndex(file, "."); i >= 0 {
		return file[i:]
	}
	return ""
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func containsInt(list []int, n int) bool {
	for _, have := range list {
		if have == n {
			return true
		}
	}
	return false
}
