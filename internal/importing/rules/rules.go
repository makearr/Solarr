// Package rules holds the checks Radarr and Sonarr make before a downloaded file is imported, as pure functions of
// facts the importer already has: a file's extension, runtime and audio, its siblings, the folder it sits in, the
// episodes it covers. Each says no with a model.Rejection and yes with nothing. Ported from their import specifications
// (ATTRIBUTION.md); where they read a database or a disk, the importer passes the answer in.
package rules

import (
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/identify/model"
)

// The rejection rules, as stored and shown: stable names, never sentences.
const (
	RuleHiddenFile         = "hidden-file"
	RuleDangerousFile      = "dangerous-file"
	RuleExecutableFile     = "executable-file"
	RuleUserRejected       = "user-rejected-extension"
	RuleArchiveFound       = "archive-found"
	RuleSample             = "sample"
	RuleSampleIndefinite   = "sample-indeterminate"
	RuleNoAudio            = "no-audio"
	RuleMultiPart          = "multi-part"
	RuleUnpacking          = "unpacking"
	RuleFullSeason         = "full-season-in-one-file"
	RuleSplitEpisode       = "split-episode"
	RuleMissingAbsolute    = "missing-absolute-number"
	RuleTitleMissing       = "episode-title-missing"
	RuleTitleTBA           = "episode-title-tba"
	RuleExistingHasMore    = "existing-file-has-more-episodes"
	RuleNotInRelease       = "not-in-grabbed-release"
	RuleUnexpectedByFolder = "unexpected-by-folder"
)

func reject(rule, field, wanted, got string) *model.Rejection {
	return &model.Rejection{Rule: rule, Field: field, Wanted: wanted, Got: got}
}

// dangerous and executable are the extensions Sonarr and Radarr refuse outright; archive ones are reported when a
// download held nothing else to import.
var (
	dangerous  = map[string]bool{".arj": true, ".lnk": true, ".lzh": true, ".ps1": true, ".scr": true, ".vbs": true, ".zipx": true}
	executable = map[string]bool{".bat": true, ".cmd": true, ".exe": true, ".sh": true}
	archives   = map[string]bool{
		".7z": true, ".bz2": true, ".gz": true, ".r00": true, ".rar": true, ".tar": true, ".tb2": true, ".tbz2": true, ".tgz": true, ".zip": true,
	}
)

// Extension says no to a dangerous or executable file, or one the user listed as rejected (extensions with or without
// the dot, any case).
func Extension(file string, userRejected []string) *model.Rejection {
	extension := strings.ToLower(path.Ext(file))
	switch {
	case dangerous[extension]:
		return reject(RuleDangerousFile, "extension", "not "+extension, extension)
	case executable[extension]:
		return reject(RuleExecutableFile, "extension", "not "+extension, extension)
	}
	for _, listed := range userRejected {
		if listed = "." + strings.ToLower(strings.Trim(strings.TrimSpace(listed), ".")); listed != "." && listed == extension {
			return reject(RuleUserRejected, "extension", "not "+extension, extension)
		}
	}
	return nil
}

// HiddenFile says no to a file whose name starts with "._", the resource forks macOS leaves beside real files.
func HiddenFile(file string) *model.Rejection {
	if strings.HasPrefix(path.Base(file), "._") {
		return reject(RuleHiddenFile, "name", "not ._*", path.Base(file))
	}
	return nil
}

// ArchiveFound says what an import that placed nothing may be missing: an archive that needs extracting, a dangerous or
// an executable file. Sonarr and Radarr report it so the person knows why nothing came of the folder.
func ArchiveFound(files []string) *model.Rejection {
	for _, file := range files {
		extension := strings.ToLower(path.Ext(file))
		switch {
		case dangerous[extension]:
			return reject(RuleDangerousFile, "extension", "not "+extension, extension)
		case executable[extension]:
			return reject(RuleExecutableFile, "extension", "not "+extension, extension)
		}
	}
	for _, file := range files {
		if archives[strings.ToLower(path.Ext(file))] {
			return reject(RuleArchiveFound, "extension", "extracted", strings.ToLower(path.Ext(file)))
		}
	}
	return nil
}

// Verdict is what the runtime says about a file being a sample.
type Verdict int

// The three answers.
const (
	VerdictReal Verdict = iota
	VerdictSample
	VerdictUnknown
)

// SampleFacts are what a sample check reads of a file.
type SampleFacts struct {
	Path string
	// Runtime is the probed length; Probed says there is one (ffprobe was missing or failed otherwise).
	Runtime time.Duration
	Probed  bool
	// ExpectedMinutes is what the movie or the episodes run for; 0 when unknown.
	ExpectedMinutes int
	// Special exempts a special episode, which may be any length.
	Special bool
}

// skipsRuntime are the files whose runtime says nothing: streams, disc images and transport streams.
var skipsRuntime = map[string]bool{".flv": true, ".strm": true, ".iso": true, ".img": true, ".m2ts": true}

// Sample says whether a file is a sample by its runtime against the shortest a real one runs (Radarr's and Sonarr's
// tiers): 15 s for a three-minute short, 90 s for a webisode, 5 min for half an hour, else 10 min. Zero length is a
// sample; no runtime at all is unknown.
func Sample(facts SampleFacts) Verdict {
	if facts.Special || skipsRuntime[strings.ToLower(path.Ext(facts.Path))] {
		return VerdictReal
	}
	if !facts.Probed {
		return VerdictUnknown
	}
	if facts.Runtime <= 0 {
		return VerdictSample
	}
	if facts.Runtime < minimumRuntime(facts.ExpectedMinutes) {
		return VerdictSample
	}
	return VerdictReal
}

func minimumRuntime(expectedMinutes int) time.Duration {
	switch {
	case expectedMinutes <= 3:
		return 15 * time.Second
	case expectedMinutes <= 10:
		return 90 * time.Second
	case expectedMinutes <= 30:
		return 5 * time.Minute
	}
	return 10 * time.Minute
}

// RejectSample turns a verdict into the rejection Radarr and Sonarr give; a file already in the library is never checked.
func RejectSample(verdict Verdict, existingFile bool) *model.Rejection {
	switch {
	case existingFile, verdict == VerdictReal:
		return nil
	case verdict == VerdictSample:
		return reject(RuleSample, "runtime", "a full-length file", "a sample")
	}
	return reject(RuleSampleIndefinite, "runtime", "a probed length", "none")
}

// HasAudio says no to a probed file with no audio track; a file that could not be probed is let through.
func HasAudio(probed bool, audioStreams int) *model.Rejection {
	if probed && audioStreams == 0 {
		return reject(RuleNoAudio, "audio", "an audio track", "none")
	}
	return nil
}

// multiPart finds the "cd1", "disc 2", "part a" tokens Radarr takes for a movie split over files. It must not be at the
// start of the name, so a title that begins with one is not read as a part.
var multiPart = []*regexp.Regexp{
	regexp.MustCompile(`(?i)[ _.-]*(?:cd|dvd|p(?:(?:ar)?t)?|dis[ck])[ _.-]*[0-9]+`),
	regexp.MustCompile(`(?i)[ _.-]*(?:cd|dvd|p(?:(?:ar)?t)?|dis[ck])[ _.-]*[a-d]+`),
}

// withoutPartTokens removes the part tokens that are not at the very start of the name.
func withoutPartTokens(re *regexp.Regexp, name string) string {
	var out strings.Builder
	last := 0
	for _, loc := range re.FindAllStringIndex(name, -1) {
		if loc[0] == 0 {
			continue
		}
		out.WriteString(name[last:loc[0]])
		last = loc[1]
	}
	out.WriteString(name[last:])
	return out.String()
}

// MultiPart says no to a movie file that looks like one part of several in its folder: its name carries a part token and
// at least one other file of the folder is the same name apart from it. siblings are the other files of the folder.
func MultiPart(file string, siblings []string) *model.Rejection {
	for _, re := range multiPart {
		own := withoutPartTokens(re, file)
		if own == file {
			continue
		}
		for _, sibling := range siblings {
			if sibling != file && withoutPartTokens(re, sibling) == own {
				return reject(RuleMultiPart, "name", "one file", "one of several parts")
			}
		}
	}
	return nil
}

// WorkingFolders are the prefixes a download client gives a folder it is still unpacking into or has failed.
var WorkingFolders = []string{"_UNPACK_", "_FAILED_"}

// Unpacking says no to a file inside a client's working folder: it is still being unpacked (or failed). A file already
// in the library is never checked.
func Unpacking(file string, existingFile bool) *model.Rejection {
	if existingFile {
		return nil
	}
	for dir := path.Dir(file); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		name := path.Base(dir)
		for _, working := range WorkingFolders {
			if strings.HasPrefix(name, working) {
				return reject(RuleUnpacking, "folder", "a finished download", name)
			}
		}
	}
	return nil
}

// CleanFolderName takes a client's working-folder markers out of a download folder's name, so "_UNPACK_Movie (2001)"
// is read as "Movie (2001)".
func CleanFolderName(name string) string {
	for _, working := range WorkingFolders {
		name = strings.ReplaceAll(name, working, "")
	}
	return name
}

// SourceFile is one video file left in a download folder, for deciding whether the folder can go.
type SourceFile struct {
	// Parsed says the name reads as a release of something; an unreadable one is kept.
	Parsed bool
	Sample bool
}

// bigArchive is the size from which a leftover rar is real content, not a stray part.
const bigArchive = 10 << 20

// FolderRemovable says a download folder may be removed after an import: every video file left is a sample, every one
// reads as a release, and no archive of a size to matter is left in it.
func FolderRemovable(videos []SourceFile, archiveSizes []int64) bool {
	for _, video := range videos {
		if !video.Parsed || !video.Sample {
			return false
		}
	}
	for _, size := range archiveSizes {
		if size > bigArchive {
			return false
		}
	}
	return true
}

// FullSeason says no to a single file whose name says it holds a whole season.
func FullSeason(nameSaysFullSeason bool) *model.Rejection {
	if nameSaysFullSeason {
		return reject(RuleFullSeason, "episodes", "one episode or a list of them", "a whole season")
	}
	return nil
}

// SplitEpisode says no to a file that is one piece of an episode split over several.
func SplitEpisode(nameSaysSplit bool) *model.Rejection {
	if nameSaysSplit {
		return reject(RuleSplitEpisode, "episodes", "a whole episode", "a piece of one")
	}
	return nil
}

// EpisodeFacts are what the TV rules read of one episode a file covers.
type EpisodeFacts struct {
	AirDate     time.Time
	Title       string
	HasAbsolute bool
}

// MissingAbsolute says no, for an anime library that names files by absolute number, to a recently aired episode that has
// none yet (the provider has not caught up). An episode that aired more than a day ago is let through.
func MissingAbsolute(anime, namesNeedAbsolute bool, episodes []EpisodeFacts, now time.Time) *model.Rejection {
	if !anime || !namesNeedAbsolute {
		return nil
	}
	for _, episode := range episodes {
		if !episode.AirDate.IsZero() && episode.AirDate.Before(now.AddDate(0, 0, -1)) {
			continue
		}
		if !episode.HasAbsolute {
			return reject(RuleMissingAbsolute, "absolute number", "an absolute number", "none, and it aired lately")
		}
	}
	return nil
}

// TitleMode is Sonarr's "Episode Title Required".
type TitleMode string

// The three modes.
const (
	TitleNever TitleMode = "never"
	// TitleBulkSeasons requires a title except for a bulk release (four or more episodes of the season on one day).
	TitleBulkSeasons TitleMode = "bulk_seasons"
	TitleAlways      TitleMode = "always"
)

// EpisodeTitle says no, when the naming uses the episode title, to a recently aired episode (48 hours) whose title is
// missing or "TBA", under the mode. sameDay is how many episodes of the season aired on the first episode's day.
func EpisodeTitle(mode TitleMode, namesUseTitle, existingFile bool, episodes []EpisodeFacts, sameDay int, now time.Time) *model.Rejection {
	if existingFile || mode == TitleNever || mode == "" || !namesUseTitle || len(episodes) == 0 {
		return nil
	}
	first := episodes[0]
	allSameDay := !first.AirDate.IsZero()
	for _, episode := range episodes {
		if !episode.AirDate.IsZero() && !episode.AirDate.Equal(first.AirDate) {
			allSameDay = false
		}
	}
	if mode == TitleBulkSeasons && allSameDay && sameDay < 4 {
		return nil
	}
	for _, episode := range episodes {
		if !episode.AirDate.IsZero() && episode.AirDate.Before(now.Add(-48*time.Hour)) {
			continue
		}
		switch strings.TrimSpace(episode.Title) {
		case "":
			return reject(RuleTitleMissing, "title", "a title", "none, and it aired lately")
		case "TBA":
			return reject(RuleTitleTBA, "title", "a title", "TBA, and it aired lately")
		}
	}
	return nil
}

// Unexpected lists what a file covers that its reference does not: the episodes a file has beyond the grabbed release,
// or beyond what its folder name says. An empty reference expects anything.
func Unexpected(reference, found []int) []int {
	if len(reference) == 0 {
		return nil
	}
	known := make(map[int]bool, len(reference))
	for _, number := range reference {
		known[number] = true
	}
	var extra []int
	for _, number := range found {
		if !known[number] {
			extra = append(extra, number)
		}
	}
	return extra
}

// NotInRelease says no to episodes the grabbed release did not hold; a file of no grab (a manual import) expects anything.
func NotInRelease(grabbed, found []int, existingFile bool) *model.Rejection {
	if existingFile {
		return nil
	}
	if extra := Unexpected(grabbed, found); len(extra) > 0 {
		return reject(RuleNotInRelease, "episodes", "those of the grabbed release", "others")
	}
	return nil
}

// ExistingHasMore says no to a file that would replace one covering more episodes than it does: each existing file of the
// episodes it covers must lie wholly within them.
func ExistingHasMore(incoming []int, existingFiles [][]int) *model.Rejection {
	covered := make(map[int]bool, len(incoming))
	for _, number := range incoming {
		covered[number] = true
	}
	for _, file := range existingFiles {
		for _, number := range file {
			if !covered[number] {
				return reject(RuleExistingHasMore, "episodes", "the same episodes", "fewer than the file on disk")
			}
		}
	}
	return nil
}
