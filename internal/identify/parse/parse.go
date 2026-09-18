// Package parse turns a music release name into structured identity and
// attribute claims (ADR-18, architecture.md section 11.1). It is the highest-risk
// component in the project: if this is wrong, nothing downstream can be right, so
// every rule here is proven against the golden corpus in testdata/releases before
// it is trusted, and confidence is reported rather than guessed silently.
package parse

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/identify/model"
)

// Identity is the release's identity claim -- media identity, never polluted by
// technical attributes (ADR-18's central rule). Scoped to what music release names
// actually carry; TV's season/episode/absolute numbering has no equivalent here and
// is not anticipated (generalise from evidence, stage 8's problem when it exists).
type Identity struct {
	Artist         string
	Album          string
	Year           int // 0 when not found
	YearEnd        int // a discography's end year; 0 otherwise
	VariousArtists bool
	Discography    bool
}

// Result is what Parse decided, plus what it could not place.
type Result struct {
	Identity   Identity
	Attributes model.Attributes
	// Confidence falls as more of the input goes unclaimed and as fewer identity
	// fields resolve. Never a formatted guess -- see confidenceOf.
	Confidence float64
	// Unparsed is every trailing token nothing claimed -- the corpus growth signal
	// (ADR-18): a release consistently landing here is a missing rule, not a shrug.
	Unparsed []string
}

var (
	yearRe        = regexp.MustCompile(`^(19|20)\d{2}$`)
	yearRangeRe   = regexp.MustCompile(`^((?:19|20)\d{2})-((?:19|20)\d{2})$`)
	bitrateWordRe = regexp.MustCompile(`(?i)^(\d{2,3})\s*k(bps)?$`)
	vbrPresetRe   = regexp.MustCompile(`(?i)^v[0-9]$`)
	discCountRe   = regexp.MustCompile(`(?i)^(\d+)\s*cd$`)
	// bitDepthRe and sampleRateRe are the two other technical claims real WEB/FLAC
	// scene tags carry bare, alongside bitrate ("24BIT", "96KHZ", "44.1KHZ") --
	// found leaking into the album title during this project's own live
	// verification pass, and now recorded on model.Attributes as their own claims
	// (todo 4.2), not just stripped.
	bitDepthRe   = regexp.MustCompile(`^(8|16|24|32)bit$`)
	sampleRateRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)khz$`)
	bracketRe    = regexp.MustCompile(`\(([^()]*)\)|\[([^\[\]]*)\]|\{([^{}]*)\}`)
	splitDashRe  = regexp.MustCompile(`\s+-\s+`)
	// dashRunRe collapses two or more dashes left adjacent by noise-bracket
	// removal (a bracket whose own contents contain a dash, e.g. "[WEB-AAC]",
	// leaves "Love-" + " " + "-" + " " + "-TDS" behind) into one -- otherwise
	// the gap reads as the human " - " artist/album separator and corrupts
	// the split, the same class of bug the WEBFLAC-token decision below guards
	// against, found this time against a real download-folder release name.
	dashRunRe = regexp.MustCompile(`(?:\s*-\s*){2,}`)
	// bracketTokenRe splits bracket contents on any separator scene names pack
	// into one bracket ("[2007/MP3/V0(VBR)]", "[Mp3~Kbps]") -- not just whitespace.
	bracketTokenRe = regexp.MustCompile(`[/~(),-]+`)
)

// Common fixed-rate MP3 encodes named bare, without a "kbps" suffix (the
// corpus's own "... (2017) 320" convention). ADR-18 sanctions this class of
// vocabulary without asking; written from well-documented, public audio format
// conventions, not copied from any single upstream source (see ATTRIBUTION.md).
var bareBitrates = map[string]int{"128": 128, "160": 160, "192": 192, "224": 224, "256": 256, "320": 320}

// losslessFormats and lossyFormats map a recognised codec token to its
// canonical name -- "tr24" is a scene alias for FLAC (Lidarr's CodecRegex
// treats it identically), so it aliases rather than storing "tr24" literally.
var losslessFormats = map[string]string{"flac": "flac", "alac": "alac", "ape": "ape", "wav": "wav", "tr24": "flac"}
var lossyFormats = map[string]string{"mp3": "mp3", "aac": "aac", "ogg": "ogg", "opus": "opus", "wma": "wma"}

// containerFormats map a recognised container token to its canonical name --
// a container, unlike a codec, does not by itself say what is inside (an
// M4A can hold lossy AAC or lossless ALAC), so it is claimed onto
// Attributes.Container rather than Attributes.Format (todo 4.2: "m4a" was
// previously misclassified as a codec in lossyFormats).
var containerFormats = map[string]string{"m4a": "m4a"}

// sourceWords maps a recognised source token to its canonical value -- most
// map to themselves, but scene abbreviations found against this project's own
// real download folder (ADR-18 corpus growth, 2026-09-04) alias to the same
// canonical source a spelled-out release would use: "VLS" (Vinyl Single) to
// "vinyl", "CDM"/"CDS" (CD Maxi/Single) to "cd".
var sourceWords = map[string]string{
	"cd": "cd", "web": "web", "vinyl": "vinyl", "cassette": "cassette",
	"sacd": "sacd", "cdrip": "cdrip", "lp": "lp",
	"vls": "vinyl", "cdm": "cd", "cds": "cd",
}

// editionWords mark a bracket or segment as noise to strip, not part of the
// album's identity: a reissue label, not a different work. Matched whole-word
// (isNoise tokenizes first) -- "ep" as a raw substring matches inside
// "Mesofisak", which is exactly the bug that made this a set of whole words
// rather than a Contains check.
var editionWords = map[string]bool{
	"edition": true, "deluxe": true, "remaster": true, "remastered": true,
	"album": true, "albums": true, "single": true, "ep": true, "promo": true,
	"anniversary": true, "bonus": true, "expanded": true, "itunes": true,
	"lossless": true, "tracks": true,
	// Found missing against this project's own real download folder --
	// "Limited Edition" and "Legacy Edition" are as common as "Deluxe
	// Edition" but were only ever half-recognised (the "edition" half).
	"limited": true, "legacy": true, "collectors": true, "collector's": true,
}

// creditPrefixes mark a collaborator credit, stripped the same way as an edition
// marker: a title's featuring credits are not part of its canonical identity.
var creditPrefixes = []string{"feat", "ft.", "ft ", "featuring", "produced"}

var variousArtistsNames = map[string]bool{"va": true, "v.a.": true, "various artists": true, "various": true}

// Normalize lowercases and collapses whitespace/underscore separators, for
// matching rather than display -- Parse itself preserves the original casing in
// Identity.
func Normalize(s string) string {
	return strings.ToLower(collapseSpaces(strings.NewReplacer("_", " ", "\t", " ", "\n", " ").Replace(s)))
}

// collapseSpaces joins runs of whitespace left behind by a removed bracket back
// into one space, without touching casing -- Normalize needs that too, but
// Identity's own fields must keep their original casing.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Parse decides what a release name identifies and claims about itself. The
// pipeline order mirrors Lidarr's own ParseAlbumTitle (ADR-18's port): reject
// outright hashed junk, remove the file extension, strip the media-type-
// independent noise regexes, normalise an embedded air date, extract the
// release group from the (still uncleaned) release title, then run this
// project's own artist/album segmentation -- kept because it is measurably
// more accurate than Lidarr's literal ReportAlbumTitleRegex cascade on real
// scene names (ATTRIBUTION.md records why).
func Parse(title string) Result {
	releaseTitle := removeFileExtension(title)
	if isHashedRelease(releaseTitle) {
		return Result{Confidence: 0, Unparsed: []string{releaseTitle}}
	}

	group := extractReleaseGroup(stripArtistPrefixForGroup(releaseTitle))
	properRepack := isProperRepack(releaseTitle)

	simple := simpleTitleRe.ReplaceAllString(releaseTitle, "")
	simple = websitePrefixRe.ReplaceAllString(simple, "")
	simple = websitePostfixRe.ReplaceAllString(simple, "")
	simple = cleanTorrentSuffixRe.ReplaceAllString(simple, "")
	simple = normalizeAirDate(simple)
	simple = normalizeSixDigitAirDate(simple)

	claims := &claimSet{}

	core := extractBrackets(simple, group, claims)
	core = dashRunRe.ReplaceAllString(core, "-")
	core = strings.TrimSpace(strings.ReplaceAll(core, "_", " "))

	var artist string
	var albumSegments []string
	if loc := splitDashRe.FindStringIndex(core); loc != nil {
		artist = strings.TrimSpace(core[:loc[0]])
		albumSegments = splitDashRe.Split(core[loc[1]:], -1)
	} else if segments := splitUnspacedDash(core); len(segments) >= 2 {
		artist = strings.TrimSpace(segments[0])
		albumSegments = segments[1:]
	} else {
		albumSegments = []string{core}
	}

	artist = collapseSpaces(artist)
	album := collapseSpaces(resolveAlbum(albumSegments, group, claims))

	identity := Identity{Artist: artist, Album: album, Year: claims.year, YearEnd: claims.yearEnd}
	if variousArtistsNames[strings.ToLower(strings.TrimSpace(artist))] {
		identity.VariousArtists = true
	}
	if strings.Contains(strings.ToLower(album), "discography") {
		identity.Discography = true
		identity.Album = "Discography"
	}

	attributes := model.Attributes{
		Format: claims.format, Lossless: claims.lossless, BitrateKbps: claims.bitrateKbps,
		BitrateMode: claims.bitrateMode, VBRPreset: claims.vbrPreset, Container: claims.container,
		Source: claims.source, Edition: claims.edition, ReleaseGroup: group,
		Discs: claims.discs, BitDepth: claims.bitDepth, SampleRate: claims.sampleRate,
		ProperRepack: properRepack,
	}

	return Result{
		Identity: identity, Attributes: attributes,
		Confidence: confidenceOf(identity, claims.unparsed, core),
		Unparsed:   claims.unparsed,
	}
}

// claimSet accumulates what bracket, segment and tail-token classification found.
type claimSet struct {
	year, yearEnd, bitrateKbps, discs, bitDepth, sampleRate    int
	format, source, edition, bitrateMode, vbrPreset, container string
	lossless                                                   bool
	unparsed                                                   []string
}

// extractBrackets removes every (...)/[...]/{...} group that classifies as noise
// (edition marker, credit, technical claim, or the release group itself --
// e.g. a real "[PMEDIA]" tag, found redundantly surviving into the album title
// even once correctly extracted as the group elsewhere); a group that
// classifies as none of those is left in place, since it is plausibly part of
// the title itself (e.g. "(Skit)").
func extractBrackets(title, group string, claims *claimSet) string {
	return bracketRe.ReplaceAllStringFunc(title, func(match string) string {
		inner := match[1 : len(match)-1]
		if group != "" && strings.EqualFold(strings.TrimSpace(inner), group) {
			return " "
		}
		if isNoise(inner, claims) {
			return " "
		}
		return match
	})
}

// isNoise reports whether a phrase (bracket contents or a dash segment) is a
// technical claim, edition marker or collaborator credit -- and if so, records
// whatever it claimed. Tokenised on more than whitespace, since scene brackets
// pack several claims into one group ("2007/MP3/V0(VBR)", "Mp3~Kbps").
func isNoise(phrase string, claims *claimSet) bool {
	lower := strings.ToLower(strings.TrimSpace(phrase))
	if lower == "" {
		return true
	}
	noise := false
	for _, prefix := range creditPrefixes {
		if strings.HasPrefix(lower, prefix) {
			noise = true
			break
		}
	}
	if !noise {
		// Whole-word membership, never strings.Contains on the raw phrase: "ep"
		// substring-matched inside "Mesofisak" and silently classified an entire
		// album title as edition noise, found against a real release name during
		// this project's own verification pass. Tokenised the same two-step way
		// (bracketTokenRe then Fields) as the vocabulary loop just below, so a
		// "/"-joined bracket like "[1993/FLAC/Lossless/Log]" still splits into
		// real words rather than one compound token that matches nothing.
		for _, token := range bracketTokenRe.Split(lower, -1) {
			for _, word := range strings.Fields(token) {
				if editionWords[word] {
					noise = true
					if claims.edition == "" {
						claims.edition = lower
					}
				}
			}
		}
	}
	// Run regardless of the verdict above: a phrase already marked noise can
	// still carry a real claim inside it ("(FLAC Lossless)" is noise because of
	// "lossless", but format=flac is still worth recording).
	for _, token := range bracketTokenRe.Split(lower, -1) {
		for _, field := range strings.Fields(token) {
			if claimField(strings.Trim(field, ",."), claims) {
				noise = true
			}
		}
	}
	return noise
}

// splitUnspacedDash splits a scene-style, no-surrounding-space "-"-joined name
// (Artist-Album-Format-Year-GROUP) into its raw segments. Used only once " - "
// (the human-readable separator) is confirmed absent.
func splitUnspacedDash(s string) []string {
	if !strings.Contains(s, "-") {
		return []string{s}
	}
	return strings.Split(s, "-")
}

// resolveAlbum turns the segments after the artist into an album title. group
// is the release group already extracted by the ADR-18 port's
// extractReleaseGroup, from the uncleaned release title -- passed in so
// stripNoiseSegments can recognise and drop it by value instead of guessing at
// segment level (see its own doc comment for why that guess used to disagree
// with the real port on an ambiguous case). Whole-phrase segment noise
// checking only applies once there is more than one segment: a single,
// unsplit remainder is exactly the case stripTrailingTokens exists for (a
// title followed by space-separated technical words), and segment-level
// isNoise's substring matching is unsound run directly against an arbitrary
// title phrase -- "Random Album Title" contains "album". A lone remainder is
// first checked for a plain-dash scene-tag tail (findUnspacedTagRun) glued on
// after the artist was already split off by " - ", e.g. an EAN-barcoded WEB
// release ("Title-5021732472793-WEB-2024-Group").
func resolveAlbum(segments []string, group string, claims *claimSet) string {
	if len(segments) == 1 {
		if head, tail, ok := findUnspacedTagRun(segments[0]); ok {
			segments = append([]string{head}, tail...)
		}
	}
	if len(segments) > 1 {
		segments = stripNoiseSegments(segments, group, claims)
		// "Artist-Year-Album-Group" puts the year right after the artist, not
		// at the end -- the only technical field this codebase has seen lead
		// rather than trail, so it is the only one checked at the front.
		if len(segments) > 1 && yearRe.MatchString(strings.TrimSpace(segments[0])) {
			claimField(strings.TrimSpace(segments[0]), claims)
			segments = segments[1:]
		}
	}
	return stripTrailingTokens(strings.TrimSpace(strings.Join(segments, " ")), claims)
}

// stripNoiseSegments drops whole trailing segments that are entirely noise (a
// technical claim or an edition marker), working from the end, and stops at
// the first segment that is genuinely part of the title. The true last field
// in a scene name is usually the release group: rather than guess at it here
// (the previous "does the segment beside it carry a real claim" heuristic
// disagreed with the actual Lidarr port on "Gotabam Bih-2007-The Sebitikoda
// Hopin-MEM" vs "Gotabam Bih-The Sebitikoda Hopin-2007-FTD" -- ATTRIBUTION.md),
// it is dropped by exact match against group, the value extractReleaseGroup
// already computed from the uncleaned title.
// stripNoiseSegments works from the end, one whole dash segment at a time:
// each is run through stripTrailingTokens' own word-level trim, and a segment
// that trims to nothing is noise in its entirety and dropped so the scan can
// continue into the one before it. The first segment that trims to something
// non-empty is the real title boundary -- its trimmed form replaces it, and
// the scan stops there, since everything before it is assumed to be genuine
// title content (a segment can be "Live At Begonobavuni Setesevug September
// 20 1968", where only the trailing "1968" is a claim; word-level trimming
// keeps the rest instead of discarding the whole segment for one embedded
// year, the bug a whole-segment any-word-matches rule had).
func stripNoiseSegments(segments []string, group string, claims *claimSet) []string {
	end := len(segments)
	if end >= 1 && group != "" {
		// The group is expected to be this segment's own first word --
		// EqualFold on the whole segment missed "ENRiCH iNT" ("iNT" marking
		// an internal/scene-only release, not part of the group itself),
		// found against this project's own real download folder.
		lastWords := strings.Fields(strings.TrimSpace(segments[end-1]))
		if len(lastWords) > 0 && strings.EqualFold(lastWords[0], group) {
			end--
		}
	}
	for end > 0 {
		trimmed := stripTrailingTokens(strings.TrimSpace(segments[end-1]), claims)
		if trimmed != "" {
			return append(append([]string{}, segments[:end-1]...), trimmed)
		}
		end--
	}
	return segments[:end]
}

// stripTrailingTokens removes recognised technical and edition words from the
// end of the album string in two passes: a single context-justified debris
// word (a trailing token that means nothing on its own but directly follows
// one that does, e.g. "... 192kbps RIP" -- RIP is dropped because 192kbps is
// real evidence beside it), then a contiguous vocabulary strip from the new
// end. Unlike group extraction, this never guesses a release group --
// word-level debris is too easily a real title word for that heuristic to be
// safe here.
func stripTrailingTokens(s string, claims *claimSet) string {
	words := strings.Fields(s)
	if len(words) >= 2 {
		last := strings.ToLower(strings.Trim(words[len(words)-1], ",."))
		secondLast := strings.ToLower(strings.Trim(words[len(words)-2], ",."))
		if !isVocabWord(last) && isStrictVocabWord(secondLast) && isPlausibleGroupTag(words[len(words)-1]) {
			words = words[:len(words)-1]
		}
	}

	end := len(words)
	recognizedAny := false
	var trimmedEdition []string // collected back-to-front, reversed below
	for end > 0 {
		field := strings.ToLower(strings.Trim(words[end-1], ",."))
		if editionWords[field] {
			trimmedEdition = append(trimmedEdition, field)
			end--
			recognizedAny = true
			continue
		}
		if !claimField(field, claims) {
			break
		}
		end--
		recognizedAny = true
	}
	// A short digit-fused-with-letters leftover beside something already
	// recognised ("12INCH" beside "VINYL", found against this project's own
	// real library) is a technical descriptor too, not title prose -- but
	// only once real evidence already justified trimming this phrase at all,
	// so an unrelated segment with nothing recognised is left untouched.
	for recognizedAny && end > 0 {
		field := strings.ToLower(strings.Trim(words[end-1], ",."))
		if !compoundTechnicalTokenRe.MatchString(field) {
			break
		}
		end--
	}
	if len(trimmedEdition) > 0 && claims.edition == "" {
		for i, j := 0, len(trimmedEdition)-1; i < j; i, j = i+1, j-1 {
			trimmedEdition[i], trimmedEdition[j] = trimmedEdition[j], trimmedEdition[i]
		}
		claims.edition = strings.Join(trimmedEdition, " ")
	}
	return strings.TrimSpace(strings.Join(words[:end], " "))
}

var compoundTechnicalTokenRe = regexp.MustCompile(`(?i)^\d+[a-z]+$`)

// isVocabWord reports whether a single lowercased word is recognised
// vocabulary -- either a technical claim (claimField) or an edition marker.
func isVocabWord(field string) bool {
	if editionWords[field] {
		return true
	}
	return isStrictVocabWord(field)
}

// isStrictVocabWord checks only claimField's technical vocabulary, excluding
// edition words -- used by the debris-word heuristic, where a common English
// word like "album" or "single" beside an unrecognised token is nowhere near
// as strong evidence as an actual bitrate or year, and treating it as such
// misread "Random Album Title" as debris after "Album" (ATTRIBUTION.md).
func isStrictVocabWord(field string) bool {
	probe := &claimSet{}
	return claimField(field, probe)
}

func isPlausibleGroupTag(word string) bool {
	if len(word) < 2 || len(word) > 12 {
		return false
	}
	for _, r := range word {
		alnum := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !alnum {
			return false
		}
	}
	return true
}

// claimField tries every vocabulary in turn against one already-lowercased word
// and records the first match.
func claimField(field string, claims *claimSet) bool {
	switch {
	case field == "":
		return true // an empty split artefact, not a real token
	case losslessFormats[field] != "":
		claims.format, claims.lossless = losslessFormats[field], true
		return true
	case lossyFormats[field] != "":
		claims.format = lossyFormats[field]
		return true
	case containerFormats[field] != "":
		claims.container = containerFormats[field]
		return true
	case sourceWords[field] != "":
		claims.source = sourceWords[field]
		return true
	case yearRangeRe.MatchString(field):
		matches := yearRangeRe.FindStringSubmatch(field)
		claims.year, _ = strconv.Atoi(matches[1])
		claims.yearEnd, _ = strconv.Atoi(matches[2])
		return true
	case yearRe.MatchString(field):
		year, _ := strconv.Atoi(field)
		switch {
		case claims.year == 0:
			claims.year = year
		case year > claims.year:
			claims.yearEnd = year
		default:
			claims.yearEnd = claims.year
			claims.year = year
		}
		return true
	case bitrateWordRe.MatchString(field):
		matches := bitrateWordRe.FindStringSubmatch(field)
		claims.bitrateKbps, _ = strconv.Atoi(matches[1])
		claims.bitrateMode = "cbr"
		return true
	case bareBitrates[field] != 0:
		claims.bitrateKbps = bareBitrates[field]
		claims.bitrateMode = "cbr"
		return true
	case vbrPresetRe.MatchString(field):
		claims.bitrateMode = "vbr"
		claims.vbrPreset = strings.ToUpper(field)
		return true
	case discCountRe.MatchString(field):
		matches := discCountRe.FindStringSubmatch(field)
		claims.discs, _ = strconv.Atoi(matches[1])
		return true
	case bitDepthRe.MatchString(field):
		matches := bitDepthRe.FindStringSubmatch(field)
		claims.bitDepth, _ = strconv.Atoi(matches[1])
		return true
	case sampleRateRe.MatchString(field):
		matches := sampleRateRe.FindStringSubmatch(field)
		khz, _ := strconv.ParseFloat(matches[1], 64)
		claims.sampleRate = int(khz * 1000)
		return true
	case barcodeRe.MatchString(field):
		// An EAN/UPC run: recognised and discarded, not recorded -- see
		// barcodeRe's own doc comment in lidarr.go.
		return true
	default:
		return false
	}
}

// confidenceOf is deliberately simple: it falls when identity did not resolve,
// and further with how much of the core string went unclaimed. Tuned against the
// golden corpus, not a formula handed down from elsewhere.
func confidenceOf(identity Identity, unparsed []string, core string) float64 {
	if identity.Artist == "" || identity.Album == "" {
		return 0.2
	}
	total := len(strings.Fields(core))
	if total == 0 {
		return 0.2
	}
	ratio := 1 - float64(len(unparsed))/float64(total)
	confidence := 0.5 + 0.5*ratio
	if confidence > 0.95 {
		confidence = 0.95 // a parse is never certain the way an embedded MBID is
	}
	if confidence < 0.2 {
		confidence = 0.2
	}
	return confidence
}
