package parse

import (
	"regexp"
	"strconv"
	"strings"
)

// The ADR-18 port of Lidarr's parser vocabulary and cascades; RE2 rewrites and deviations: documentation/music-identification.md#release-name-parser.

// isHashedRelease ports Lidarr's RejectHashedReleasesRegex -- release names that
// are actually just a hash or a known junk pattern, never real identity. None of
// the seven patterns use lookaround.
var hashedReleasePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^[0-9a-zA-Z]{32}`),
	regexp.MustCompile(`^[a-z0-9]{24}$`),
	regexp.MustCompile(`^[A-Z]{11}\d{3}$`),
	regexp.MustCompile(`^[a-z]{12}\d{3}$`),
	regexp.MustCompile(`^Backup_\d{5,}S\d{2}-\d{2}$`),
	regexp.MustCompile(`^123$`),
	regexp.MustCompile(`(?i)^abc$`),
	regexp.MustCompile(`(?i)^b00bs$`),
}

// isHashedRelease reports whether title is a junk/hash filename with no real
// identity to extract -- checked before anything else, matching Lidarr's
// ValidateBeforeParsing.
func isHashedRelease(title string) bool {
	for _, re := range hashedReleasePatterns {
		if re.MatchString(title) {
			return true
		}
	}
	return false
}

// Shared cleanup regexes (the 21 media-type-independent group). None use
// lookaround except percentRe and normalizeArticleRe, handled separately below.
var (
	// simpleTitleRe strips video-quality tokens that occasionally leak into music
	// release names shared with video scene conventions (resolution, x264/x265,
	// bit-depth-as-video-term). Ported from SimpleTitleRegex.
	simpleTitleRe = regexp.MustCompile(`(?i)(?:(480|720|1080|2160|320)[ip]|[xh][\W_]?26[45]|DD\W?5\W1|[<>*|]|848x480|1280x720|1920x1080|3840x2160|4096x2160|(8|10)b(it)?)\s*`)

	websitePrefixRe  = regexp.MustCompile(`(?i)^(?:\[\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?:[a-z]{2,6}\.[a-z]{2,6}|xn--[a-z0-9-]{4,}|[a-z]{2,})\b(?:\s*\]|[ -]{2,})[ -]*`)
	websitePostfixRe = regexp.MustCompile(`(?i)(?:\[\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?:xn--[a-z0-9-]{4,}|[a-z]{2,6})\b(?:\s*\])$`)

	cleanTorrentSuffixRe = regexp.MustCompile(`(?i)\[(?:ettv|rartv|rarbg|cttv)\]$`)

	releaseTokenPrefixRe = regexp.MustCompile(`(?i)^(\[(TR24|OF)\])*\s*`)

	fileExtensionRe = regexp.MustCompile(`(?i)\.[a-z0-9]{2,4}$`)

	// commonTagCreditRe strips a featuring/production credit, bracket-bounded or
	// not. Ported from CommonTagRegex[0].
	commonTagCreditRe = regexp.MustCompile(`(?i)(\[|\()*\b(?:featuring|feat\.|feat|ft|ft\.|produced by)\s.*(\]|\))*`)
	// commonTagEditionRe strips a bracketed edition marker. Ported from
	// CommonTagRegex[1] -- CleanAlbumTitle's own regex.
	commonTagEditionRe = regexp.MustCompile(`(?i)(?:\(|\[)(?:[^(\[]*)(?:version|limited|deluxe|single|clean|album|special|bonus|promo|remaster(?:ed)?)(?:[^)\]]*)(?:\)|\])`)

	bracketParenRe  = regexp.MustCompile(`\(.*\)`)
	bracketSquareRe = regexp.MustCompile(`\[.*\]`)
	afterDashRe     = regexp.MustCompile(`[-:].*`)

	wordDelimiterRe  = regexp.MustCompile(`(\s|\.|,|_|-|=|\|)+`)
	punctuationRe    = regexp.MustCompile(`[^\w\s]`)
	commonWordRe     = regexp.MustCompile(`(?i)\b(a|an|the|and|or|of)\b\s?`)
	duplicateSpaceRe = regexp.MustCompile(`\s{2,}`)

	// cleanReleaseGroupSuffixRe strips known junk suffixes before group extraction; the leading-artist alternative is not carried (documentation/music-identification.md#release-name-parser).
	cleanReleaseGroupSuffixRe = regexp.MustCompile(`(?i)(?:-(?:RP|1|NZBGeek|Obfuscated|Scrambled|sample|Pre|postbot|xpost|Rakuv[a-z0-9]*|WhiteRev|BUYMORE|AsRequested|AlternativeToRequested|GEROV|Z0iDS3N|Chamele0n|4P|4Planet|AlteZachen|RePACKPOST))+$`)

	// exceptionReleaseGroupExactRe has no lookaround in the original -- ported
	// directly. A short manual allow-list of groups whose own name would
	// otherwise defeat ReleaseGroupRegex's heuristics.
	exceptionReleaseGroupExactRe = regexp.MustCompile(`(?i)(D-Z0N3|Fight-BB|VARYG|E\.N\.D)\b`)
)

// knownAnimeMarkerRe is used only to decide whether the anime-bracket cascade
// step is worth attempting -- title starts with a bracketed subgroup tag.
var animeReleaseGroupRe = regexp.MustCompile(`^\[([^\s].*?[^\s])\](?:_|-|\s|\.)?`)

// exceptionReleaseGroupWords is ExceptionReleaseGroupRegex's vocabulary; its lookarounds are an explicit boundary check (isSeparatorByte, isCloserByte).
var exceptionReleaseGroupWords = []string{
	"Silence", "afm72", "Panda", "Ghost", "MONOLITH", "Tigole", "Joy", "ImE", "UTR",
	"t3nzin", "Anime Time", "Project Angel", "Hakata Ramen", "HONE", "Vyndros",
	"SEV", "Garshasp", "Kappa", "Natty", "RCVR", "SAMPA", "YOGI", "r00t",
	"EDGE2020", "RZeroX",
}

func isSeparatorByte(b byte) bool {
	return b == '.' || b == '_' || b == ' ' || b == '['
}

func isCloserByte(b byte) bool {
	return b == ']' || b == ')'
}

// exceptionReleaseGroup ports ExceptionReleaseGroupRegex: a fixed group name is
// only trusted when a separator/bracket precedes it and `]`/`)` follows --
// otherwise "Ghost" inside an ordinary title would false-positive.
func exceptionReleaseGroup(title string) string {
	best := ""
	lower := strings.ToLower(title)
	for _, word := range exceptionReleaseGroupWords {
		needle := strings.ToLower(word)
		for start := 0; ; {
			idx := strings.Index(lower[start:], needle)
			if idx < 0 {
				break
			}
			pos := start + idx
			end := pos + len(needle)
			precededOK := pos > 0 && isSeparatorByte(title[pos-1])
			followedOK := end < len(title) && isCloserByte(title[end])
			if precededOK && followedOK {
				best = title[pos:end] // last match wins, matching .Last()
			}
			start = pos + 1
		}
	}
	return best
}

// releaseGroupCandidateRe finds `-token` occurrences for the two-pass rewrite of
// ReleaseGroupRegex below.
var releaseGroupCandidateRe = regexp.MustCompile(`(?i)-([a-z0-9]+)`)
var releaseGroupBracketRe = regexp.MustCompile(`(?i)[-._ ]\[([a-z0-9]+)\]$`)
var codecWordRe = regexp.MustCompile(`(?i)MP3|ALAC|FLAC|WEB`)

// codecWordExact names the values that are a codec or source and never a release group ("... [FLAC]" has no group).
var codecWordExact = map[string]bool{"mp3": true, "alac": true, "flac": true, "web": true}

// releaseGroupTag is ReleaseGroupRegex as a two-pass match that keeps the last candidate with no codec word after it.
func releaseGroupTag(title string) string {
	type candidate struct {
		start, end int
		value      string
	}
	var candidates []candidate
	for _, m := range releaseGroupCandidateRe.FindAllStringSubmatchIndex(title, -1) {
		end := m[3]
		value := title[m[2]:m[3]]
		if codecWordRe.MatchString(title[end:]) || codecWordExact[strings.ToLower(value)] {
			continue
		}
		candidates = append(candidates, candidate{m[2], end, value})
	}
	if m := releaseGroupBracketRe.FindStringSubmatchIndex(title); m != nil {
		value := title[m[2]:m[3]]
		// A bracket naming a format or bitrate ("[320kbps]", "[M4A]", "[DSD64]") is a claim, not a group.
		if !codecWordExact[strings.ToLower(value)] && !isVocabWord(strings.ToLower(value)) {
			candidates = append(candidates, candidate{m[2], m[3], value})
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	last := candidates[0]
	for _, c := range candidates[1:] {
		if c.start >= last.start {
			last = c
		}
	}
	if _, err := strconv.Atoi(last.value); err == nil {
		return "" // ParseReleaseGroup discards a purely numeric "group"
	}
	return last.value
}

// stripArtistPrefixForGroup removes the artist first so a hyphenated artist name does not collide with the dash-token scan.
func stripArtistPrefixForGroup(s string) string {
	if loc := splitDashRe.FindStringIndex(s); loc != nil {
		return s[loc[1]:]
	}
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		return s[idx+1:]
	}
	return s
}

// extractReleaseGroup is ParseReleaseGroup's cascade; title must already lack the extension and the artist portion.
func extractReleaseGroup(title string) string {
	title = strings.TrimSpace(title)
	title = websitePrefixRe.ReplaceAllString(title, "")
	title = releaseTokenPrefixRe.ReplaceAllString(title, "")

	if m := animeReleaseGroupRe.FindStringSubmatch(title); m != nil {
		return strings.TrimSpace(m[1])
	}

	title = cleanReleaseGroupSuffixRe.ReplaceAllString(title, "")

	if group := exceptionReleaseGroup(title); group != "" {
		return group
	}
	if m := exceptionReleaseGroupExactRe.FindAllString(title, -1); len(m) > 0 {
		return m[len(m)-1]
	}
	return releaseGroupTag(title)
}

// removeFileExtension ports Parser.RemoveFileExtension: only a known media or
// sidecar extension is stripped, so a release whose title ends "...Vol. 1" is
// left alone.
var knownExtensions = map[string]bool{
	".flac": true, ".mp3": true, ".m4a": true, ".m4b": true, ".m4p": true,
	".aac": true, ".ogg": true, ".oga": true, ".opus": true, ".wav": true,
	".wma": true, ".ape": true, ".wv": true, ".alac": true,
	".nzb": true, ".par2": true,
}

func removeFileExtension(title string) string {
	loc := fileExtensionRe.FindStringIndex(title)
	if loc == nil {
		return title
	}
	if knownExtensions[strings.ToLower(title[loc[0]:loc[1]])] {
		return title[:loc[0]]
	}
	return title
}

// percentRe two-passes PercentRegex (`(?<=\b\d+)%`, lookbehind): the lookbehind
// only asserted digits precede the `%`, which the capturing group already
// requires, so replacing the whole `digits%` match is exactly equivalent.
var percentRe = regexp.MustCompile(`\b(\d+)%`)

// fillerWordRe and nonWordRe two-pass NormalizeRegex: a filler word that is the whole leading or trailing token is skipped, not removed.
var (
	fillerWordRe = regexp.MustCompile(`(?i)\b(a|à|an|the|and|or|of)\b`)
	nonWordRe    = regexp.MustCompile(`[^\w]`)
)

// CleanArtistName is a lowercase punctuation-free lookup key without filler words; a purely numeric name is returned unchanged.
func CleanArtistName(name string) string {
	if _, err := strconv.Atoi(name); err == nil {
		return name
	}
	normalized := strings.ReplaceAll(name, "_", " ")
	normalized = replacePercent(normalized)

	var b strings.Builder
	last := 0
	for _, m := range fillerWordRe.FindAllStringIndex(normalized, -1) {
		if m[0] == 0 || m[1] == len(normalized) {
			continue
		}
		b.WriteString(normalized[last:m[0]])
		last = m[1]
	}
	b.WriteString(normalized[last:])

	return strings.ToLower(nonWordRe.ReplaceAllString(b.String(), ""))
}

func replacePercent(s string) string {
	return percentRe.ReplaceAllString(s, "${1}percent")
}

// NormalizeTitle collapses delimiters, drops punctuation and filler words for loose title keys, unlike this package's Normalize.
func NormalizeTitle(title string) string {
	title = wordDelimiterRe.ReplaceAllString(title, " ")
	title = punctuationRe.ReplaceAllString(title, "")
	title = commonWordRe.ReplaceAllString(title, "")
	title = duplicateSpaceRe.ReplaceAllString(title, " ")
	return strings.ToLower(strings.TrimSpace(title))
}

// CleanAlbumTitle ports Parser.CleanAlbumTitle: strips a bracketed edition
// marker CommonTagRegex[1] recognises ("(Deluxe Edition)", "[Remastered]").
func CleanAlbumTitle(album string) string {
	return strings.TrimSpace(commonTagEditionRe.ReplaceAllString(album, ""))
}

// CleanTrackTitle ports Parser.CleanTrackTitle: strips a featuring/production
// credit, then the same bracketed edition marker CleanAlbumTitle strips.
func CleanTrackTitle(title string) string {
	title = commonTagCreditRe.ReplaceAllString(title, "")
	title = commonTagEditionRe.ReplaceAllString(title, "")
	return strings.TrimSpace(title)
}

// RemoveBracketsAndContents ports Parser.RemoveBracketsAndContents.
func RemoveBracketsAndContents(s string) string {
	s = bracketParenRe.ReplaceAllString(s, "")
	s = bracketSquareRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// RemoveAfterDash ports Parser.RemoveAfterDash.
func RemoveAfterDash(s string) string {
	return strings.TrimSpace(afterDashRe.ReplaceAllString(s, ""))
}

// airDateRe and sixDigitAirDateRe check the bytes around the date in place of the original's lookaround, so a longer digit run is not a date.
var (
	airDateRe         = regexp.MustCompile(`^(.*?)((\d{4})[_.-]([0-1][0-9])[_.-]([0-3][0-9])|([0-1][0-9])[_.-]([0-3][0-9])[_.-](\d{4}))`)
	sixDigitAirDateRe = regexp.MustCompile(`[_.-]([1-9]\d)([0-1][0-9])([0-3][0-9])[_.-]`)
)

// normalizeAirDate rewrites an embedded YYYY-MM-DD or MM-DD-YYYY date to YYYY.MM.DD for dated live recordings; space-separated dates are left alone.
func normalizeAirDate(s string) string {
	m := airDateRe.FindStringSubmatchIndex(s)
	if m == nil {
		return s
	}
	dateStart, dateEnd := m[4], m[5]
	digitBefore := dateStart > 0 && isDigit(s[dateStart-1])
	digitAfter := dateEnd < len(s) && isDigit(s[dateEnd])
	if digitBefore || digitAfter {
		return s // the original lookaround would have refused this match too
	}
	prefix := s[m[2]:m[3]]
	var year, month, day string
	if m[6] != -1 {
		year, month, day = s[m[6]:m[7]], s[m[8]:m[9]], s[m[10]:m[11]]
	} else {
		month, day, year = s[m[12]:m[13]], s[m[14]:m[15]], s[m[16]:m[17]]
	}
	return prefix + year + "." + month + "." + day
}

// normalizeSixDigitAirDate ports SixDigitAirDateRegex's substitution: a bare
// YYMMDD run between separators becomes "20" + YY + "." + MM + "." + DD, unless
// both month and day are "00" (a version number, not a date -- e.g. "100200").
func normalizeSixDigitAirDate(s string) string {
	m := sixDigitAirDateRe.FindStringSubmatchIndex(s)
	if m == nil {
		return s
	}
	year, month, day := s[m[2]:m[3]], s[m[4]:m[5]], s[m[6]:m[7]]
	if month == "00" && day == "00" {
		return s
	}
	return s[:m[2]] + "20" + year + "." + month + "." + day + s[m[7]:]
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// Quality-modifier vocabulary; versionRe's lookbehind becomes a digit-before-v match that checks the three preceding bytes so "MP3" is not a version.
var (
	properRe         = regexp.MustCompile(`(?i)\b(proper)\b`)
	repackRe         = regexp.MustCompile(`(?i)\b(repack\d?|rerip\d?)\b`)
	realRe           = regexp.MustCompile(`\b(REAL)\b`)
	versionDigitVRe  = regexp.MustCompile(`(?i)\dv(\d)[-._ ]`)
	versionBracketRe = regexp.MustCompile(`(?i)\[v(\d)\]`)
	versionRepackRe  = regexp.MustCompile(`(?i)repack(\d)`)
	versionReripRe   = regexp.MustCompile(`(?i)rerip(\d)`)
)

// parseVersion ports VersionRegex's four alternatives in priority order,
// applying the MP3-exclusion filter only to the first (digit-before-"v") form,
// the only one the lookbehind guarded.
func parseVersion(lower string) (int, bool) {
	if m := versionDigitVRe.FindAllStringSubmatchIndex(lower, -1); m != nil {
		for _, idx := range m {
			digitPos := idx[0]
			if digitPos >= 3 && lower[digitPos-3:digitPos] == "mp3" {
				continue
			}
			v, _ := strconv.Atoi(lower[idx[2]:idx[3]])
			return v, true
		}
	}
	if m := versionBracketRe.FindStringSubmatch(lower); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v, true
	}
	if m := versionRepackRe.FindStringSubmatch(lower); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v, true
	}
	if m := versionReripRe.FindStringSubmatch(lower); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v, true
	}
	return 0, false
}

// isProperRepack folds the three revision signals into the one flag model.Attributes keeps (a corrected re-release).
func isProperRepack(name string) bool {
	lower := strings.ToLower(name)
	if properRe.MatchString(lower) || repackRe.MatchString(lower) {
		return true
	}
	if _, ok := parseVersion(lower); ok {
		return true
	}
	return realRe.MatchString(name)
}

// barcodeRe discards a bare EAN/UPC run from the title; the value is not stored (EnrichJob sets it once a release is matched).
var barcodeRe = regexp.MustCompile(`^\d{6,14}$`)

// findUnspacedTagRun finds a scene-tag tail glued on by plain dashes, only at a dash whose next token is recognised vocabulary or a barcode.
func findUnspacedTagRun(s string) (head string, tail []string, ok bool) {
	parts := strings.Split(s, "-")
	if len(parts) < 2 {
		return "", nil, false
	}
	for i := 1; i < len(parts); i++ {
		token := strings.ToLower(strings.TrimSpace(parts[i]))
		if token == "" || strings.Contains(token, " ") {
			continue
		}
		// A year range such as "1969-1982" is one claim: skip past it, and keep looking for a tag run further along.
		prevWords := strings.Fields(parts[i-1])
		if len(prevWords) > 0 && yearRe.MatchString(prevWords[len(prevWords)-1]) && yearRe.MatchString(token) {
			continue
		}
		if isVocabWord(token) || barcodeRe.MatchString(token) {
			return strings.TrimSpace(strings.Join(parts[:i], "-")), parts[i:], true
		}
	}
	return "", nil, false
}
