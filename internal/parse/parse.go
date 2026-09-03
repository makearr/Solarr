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

	"github.com/makearr/MediaNexus/internal/model"
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
	bracketRe     = regexp.MustCompile(`\(([^()]*)\)|\[([^\[\]]*)\]|\{([^{}]*)\}`)
	splitDashRe   = regexp.MustCompile(`\s+-\s+`)
	// bracketTokenRe splits bracket contents on any separator scene names pack
	// into one bracket ("[2007/MP3/V0(VBR)]", "[Mp3~Kbps]") -- not just whitespace.
	bracketTokenRe = regexp.MustCompile(`[/~(),-]+`)
)

// Common fixed-rate MP3 encodes named bare, without a "kbps" suffix (the
// corpus's own "... (2017) 320" convention). ADR-18 sanctions this class of
// vocabulary without asking; written from well-documented, public audio format
// conventions, not copied from any single upstream source (see ATTRIBUTION.md).
var bareBitrates = map[string]int{"128": 128, "160": 160, "192": 192, "224": 224, "256": 256, "320": 320}

var losslessFormats = map[string]bool{"flac": true, "alac": true, "ape": true, "wav": true}
var lossyFormats = map[string]bool{"mp3": true, "aac": true, "m4a": true, "ogg": true, "opus": true, "wma": true}
var sourceWords = map[string]bool{"cd": true, "web": true, "vinyl": true, "cassette": true, "sacd": true, "cdrip": true}

// editionWords mark a bracket or segment as noise to strip, not part of the
// album's identity: a reissue label, not a different work.
var editionWords = []string{
	"edition", "deluxe", "remaster", "album", "single", "ep", "promo",
	"anniversary", "bonus", "expanded", "itunes", "lossless", "tracks",
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

// Parse decides what a release name identifies and claims about itself.
func Parse(title string) Result {
	claims := &claimSet{}

	core := extractBrackets(title, claims)
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
	album := collapseSpaces(resolveAlbum(albumSegments, claims))

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
		Source: claims.source, Edition: claims.edition, ReleaseGroup: claims.releaseGroup,
		Discs: claims.discs,
	}

	return Result{
		Identity: identity, Attributes: attributes,
		Confidence: confidenceOf(identity, claims.unparsed, core),
		Unparsed:   claims.unparsed,
	}
}

// claimSet accumulates what bracket, segment and tail-token classification found.
type claimSet struct {
	year, yearEnd, bitrateKbps, discs int
	format, source, edition           string
	releaseGroup                      string
	lossless                          bool
	unparsed                          []string
}

// extractBrackets removes every (...)/[...]/{...} group that classifies as noise
// (edition marker, credit, technical claim); a group that classifies as neither is
// left in place, since it is plausibly part of the title itself (e.g. "(Skit)").
func extractBrackets(title string, claims *claimSet) string {
	return bracketRe.ReplaceAllStringFunc(title, func(match string) string {
		inner := match[1 : len(match)-1]
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
		for _, word := range editionWords {
			if strings.Contains(lower, word) {
				noise = true
				if claims.edition == "" {
					claims.edition = lower
				}
				break
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

// resolveAlbum turns the segments after the artist into an album title.
// Whole-phrase segment noise checking only applies once there is more than one
// segment: a single, unsplit remainder is exactly the case stripTrailingTokens
// exists for (a title followed by space-separated technical words), and
// segment-level isNoise's substring matching is unsound run directly against an
// arbitrary title phrase -- "Random Album Title" contains "album".
func resolveAlbum(segments []string, claims *claimSet) string {
	if len(segments) > 1 {
		segments = stripNoiseSegments(segments, claims)
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
// technical claim or an edition marker), working from the end, and stops at the
// first segment that is genuinely part of the title. The true last field in a
// scene name is usually the release group, which by definition matches no
// vocabulary of its own -- so it is only recognised by looking one segment
// further in: if the segment beside it does carry a real claim, the group tag is
// real evidence too, the same context-justified shape stripTrailingTokens uses
// for a trailing debris word.
func stripNoiseSegments(segments []string, claims *claimSet) []string {
	end := len(segments)
	if end >= 2 && claims.releaseGroup == "" {
		last := strings.TrimSpace(segments[end-1])
		prior := strings.TrimSpace(segments[end-2])
		if !isNoise(last, &claimSet{}) && isNoise(prior, &claimSet{}) && isPlausibleGroupTag(last) {
			claims.releaseGroup = last
			end--
		}
	}
	for end > 0 {
		if !isNoise(strings.TrimSpace(segments[end-1]), claims) {
			break
		}
		end--
	}
	return segments[:end]
}

// stripTrailingTokens removes recognised technical words from the end of the
// album string in two passes: a single context-justified debris word (a trailing
// token that means nothing on its own but directly follows one that does, e.g.
// "... 192kbps RIP" -- RIP is dropped because 192kbps is real evidence beside
// it), then a contiguous vocabulary strip from the new end. Unlike
// stripNoiseSegments, this never guesses a release group -- word-level debris is
// too easily a real title word for that heuristic to be safe here.
func stripTrailingTokens(s string, claims *claimSet) string {
	words := strings.Fields(s)
	if len(words) >= 2 {
		last := strings.ToLower(strings.Trim(words[len(words)-1], ",."))
		secondLast := strings.ToLower(strings.Trim(words[len(words)-2], ",."))
		if !isVocabWord(last) && isVocabWord(secondLast) && isPlausibleGroupTag(words[len(words)-1]) {
			words = words[:len(words)-1]
		}
	}

	end := len(words)
	for end > 0 {
		field := strings.ToLower(strings.Trim(words[end-1], ",."))
		if !claimField(field, claims) {
			break
		}
		end--
	}
	return strings.TrimSpace(strings.Join(words[:end], " "))
}

func isVocabWord(field string) bool {
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
	case losslessFormats[field]:
		claims.format, claims.lossless = field, true
		return true
	case lossyFormats[field]:
		claims.format = field
		return true
	case sourceWords[field]:
		claims.source = field
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
		return true
	case bareBitrates[field] != 0:
		claims.bitrateKbps = bareBitrates[field]
		return true
	case vbrPresetRe.MatchString(field):
		claims.bitrateKbps = -1 // VBR: a preset, not a fixed rate; negative marks "variable"
		return true
	case discCountRe.MatchString(field):
		matches := discCountRe.FindStringSubmatch(field)
		claims.discs, _ = strconv.Atoi(matches[1])
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
