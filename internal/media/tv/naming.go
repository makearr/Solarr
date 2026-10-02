package tv

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// Naming tokens, Sonarr's spelling (ADR-23: TV's vocabulary, never core's).
const (
	TokenSeriesTitle         = "Series Title"
	TokenSeriesTitleYear     = "Series TitleYear"
	TokenSeriesTitleThe      = "Series TitleThe"
	TokenSeriesTitleTheYear  = "Series TitleTheYear"
	TokenSeriesCleanTitle    = "Series CleanTitle"
	TokenSeriesCleanTitleThe = "Series CleanTitleThe"
	TokenSeriesYear          = "Series Year"
	TokenSeason              = "season"
	TokenEpisode             = "episode"
	TokenAbsolute            = "absolute"
	TokenEpisodeTitle        = "Episode Title"
	TokenEpisodeCleanTitle   = "Episode CleanTitle"
	TokenAirDate             = "Air-Date"
	TokenAirDateSpaced       = "Air Date"
	TokenQualityFull         = "Quality Full"
	TokenQualityTitle        = "Quality Title"
	TokenVideoCodec          = "MediaInfo VideoCodec"
	TokenVideoDynamicRange   = "MediaInfo VideoDynamicRangeType"
	TokenAudioCodec          = "MediaInfo AudioCodec"
	TokenAudioChannels       = "MediaInfo AudioChannels"
	TokenAudioLanguages      = "MediaInfo AudioLanguages"
	TokenSubtitleLanguages   = "MediaInfo SubtitleLanguages"
	TokenReleaseGroup        = "Release Group"
	TokenRevisionVersion     = "Revision Version"
	TokenOriginalTitle       = "Original Title"
	TokenOriginalFilename    = "Original Filename"
	TokenTvdbID              = "TvdbId"
	TokenTmdbID              = "TmdbId"
	TokenImdbID              = "ImdbId"
	TokenTvMazeID            = "TvMazeId"
)

// Naming slots, Sonarr's six formats.
const (
	SlotStandardEpisode = "standard_episode"
	SlotDailyEpisode    = "daily_episode"
	SlotAnimeEpisode    = "anime_episode"
	SlotSeriesFolder    = "series_folder"
	SlotSeasonFolder    = "season_folder"
	SlotSpecialsFolder  = "specials_folder"
)

// MultiEpisodeStyle is how one file covering several episodes writes its numbers (Sonarr's "Multi-Episode Style").
type MultiEpisodeStyle string

// The six styles, as Sonarr names them; Prefixed Range is Sonarr's own default.
const (
	StyleExtend        MultiEpisodeStyle = "extend"
	StyleDuplicate     MultiEpisodeStyle = "duplicate"
	StyleRepeat        MultiEpisodeStyle = "repeat"
	StyleScene         MultiEpisodeStyle = "scene"
	StyleRange         MultiEpisodeStyle = "range"
	StylePrefixedRange MultiEpisodeStyle = "prefixed_range"
)

// Valid reports whether the style is one of the six.
func (s MultiEpisodeStyle) Valid() bool {
	switch s {
	case StyleExtend, StyleDuplicate, StyleRepeat, StyleScene, StyleRange, StylePrefixedRange:
		return true
	}
	return false
}

// NamingScheme is TV's naming declaration. The first preset reproduces the user's own library byte for byte.
var NamingScheme = naming.Scheme{
	Slots: []naming.Slot{
		{Key: SlotStandardEpisode, Label: "Standard Episode Format", Help: "An episode of a series with seasons. A / starts a folder."},
		{Key: SlotDailyEpisode, Label: "Daily Episode Format", Help: "An episode of a series that airs by date."},
		{Key: SlotAnimeEpisode, Label: "Anime Episode Format", Help: "An episode of an anime series."},
		{Key: SlotSeriesFolder, Label: "Series Folder Format", Help: "The folder each series goes in."},
		{Key: SlotSeasonFolder, Label: "Season Folder Format", Help: "The folder each season goes in, below the series folder."},
		{Key: SlotSpecialsFolder, Label: "Specials Folder Format", Help: "The folder season 0 goes in, below the series folder."},
	},
	Tokens: []naming.Token{
		{Spelling: "{Series Title}", Group: "Series", Example: "The Series Title's!", Help: "The series' title."},
		{Spelling: "{Series TitleYear}", Group: "Series", Example: "The Series Title's! (2010)", Help: "The title with its year, unless the title already ends in one."},
		{Spelling: "{Series TitleThe}", Group: "Series", Example: "Series Title's!, The", Help: "The title with a leading The, A or An moved to the end."},
		{Spelling: "{Series TitleTheYear}", Group: "Series", Example: "Series Title's!, The (2010)", Help: "The title with its article moved to the end, then the year."},
		{Spelling: "{Series CleanTitle}", Group: "Series", Example: "The Series Titles", Help: "The title without punctuation, & written as and."},
		{Spelling: "{Series CleanTitleThe}", Group: "Series", Example: "Series Titles, The", Help: "The clean title with its leading article moved to the end."},
		{Spelling: "{Series Year}", Group: "Series", Example: "2010", Help: "The year the series first aired."},
		{Spelling: "{season}", Group: "Season", Example: "1", Help: "The season number."},
		{Spelling: "{season:00}", Group: "Season", Example: "01", Help: "The season number, two digits."},
		{Spelling: "{episode}", Group: "Episode", Example: "1", Help: "The episode number."},
		{Spelling: "{episode:00}", Group: "Episode", Example: "01", Help: "The episode number, two digits."},
		{Spelling: "{absolute}", Group: "Episode", Example: "1", Help: "The running episode number across seasons; anime numbering."},
		{Spelling: "{absolute:000}", Group: "Episode", Example: "001", Help: "The absolute number, three digits."},
		{Spelling: "{Episode Title}", Group: "Episode", Example: "Episode's Title", Help: "The episode's title; several episodes' titles are joined with +."},
		{Spelling: "{Episode CleanTitle}", Group: "Episode", Example: "Episodes Title", Help: "The title without punctuation, & written as and."},
		{Spelling: "{Air-Date}", Group: "Air date", Example: "2016-03-20", Help: "The air date, year-month-day."},
		{Spelling: "{Air Date}", Group: "Air date", Example: "2016 03 20", Help: "The air date with spaces."},
		{Spelling: "{Quality Full}", Group: "Quality", Example: "WEBDL-1080p Proper", Help: "The source and resolution the name claims, with Proper or Repack."},
		{Spelling: "{Quality Title}", Group: "Quality", Example: "WEBDL-1080p", Help: "The source and resolution the name claims."},
		{Spelling: "{MediaInfo VideoCodec}", Group: "Media info", Example: "x264", Help: "The video codec measured from the file."},
		{Spelling: "{MediaInfo VideoDynamicRangeType}", Group: "Media info", Example: "HDR", Help: "HDR, DV or HLG when the file carries it."},
		{Spelling: "{MediaInfo AudioCodec}", Group: "Media info", Example: "EAC3", Help: "The audio codec measured from the file."},
		{Spelling: "{MediaInfo AudioChannels}", Group: "Media info", Example: "5.1", Help: "The audio channels measured from the file."},
		{Spelling: "{MediaInfo AudioLanguages}", Group: "Media info", Example: "EN", Help: "The audio languages measured from the file."},
		{Spelling: "{MediaInfo SubtitleLanguages}", Group: "Media info", Example: "EN", Help: "The subtitle languages measured from the file."},
		{Spelling: "{Release Group}", Group: "Release", Example: "GROUP", Help: "The release group the name credits."},
		{Spelling: "{Revision Version}", Group: "Release", Example: "v2", Help: "v2 and up for a PROPER, REPACK or revised release."},
		{Spelling: "{Original Title}", Group: "Original", Example: "Series.Title.S01E01.720p.HDTV-GROUP", Help: "The release name the file arrived with."},
		{Spelling: "{Original Filename}", Group: "Original", Example: "series.title.s01e01", Help: "The name the file arrived with, without its extension."},
		{Spelling: "{TvdbId}", Group: "Ids", Example: "12345", Help: "The series' TheTVDB id."},
		{Spelling: "{TmdbId}", Group: "Ids", Example: "67890", Help: "The series' TMDB id."},
		{Spelling: "{ImdbId}", Group: "Ids", Example: "tt1234567", Help: "The series' IMDb id."},
		{Spelling: "{TvMazeId}", Group: "Ids", Example: "321", Help: "The series' TVmaze id."},
	},
	Presets: []naming.Preset{
		{Name: "Solarr default", Formats: map[string]string{
			SlotStandardEpisode: "{Series Title} - S{season:00}E{episode:00} - {Episode Title}",
			SlotDailyEpisode:    "{Series Title} - {Air-Date} - {Episode Title}",
			SlotAnimeEpisode:    "{Series Title} - S{season:00}E{episode:00} - {Episode Title}",
			SlotSeriesFolder:    "{Series Title}",
			SlotSeasonFolder:    "Season {season}",
			SlotSpecialsFolder:  "Specials",
		}},
		{Name: "Sonarr default", Formats: map[string]string{
			SlotStandardEpisode: "{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}",
			SlotDailyEpisode:    "{Series Title} - {Air-Date} - {Episode Title} {Quality Full}",
			SlotAnimeEpisode:    "{Series Title} - S{season:00}E{episode:00} - {absolute:000} - {Episode Title} {Quality Full}",
			SlotSeriesFolder:    "{Series Title}",
			SlotSeasonFolder:    "Season {season}",
			SlotSpecialsFolder:  "Specials",
		}},
		{Name: "Plex", Formats: map[string]string{
			SlotStandardEpisode: "{Series TitleYear} - S{season:00}E{episode:00} - {Episode Title}",
			SlotDailyEpisode:    "{Series TitleYear} - {Air-Date} - {Episode Title}",
			SlotAnimeEpisode:    "{Series TitleYear} - S{season:00}E{episode:00} - {Episode Title}",
			SlotSeriesFolder:    "{Series TitleYear} {{tvdb-{TvdbId}}}",
			SlotSeasonFolder:    "Season {season:00}",
			SlotSpecialsFolder:  "Specials",
		}},
		{Name: "Jellyfin", Formats: map[string]string{
			SlotStandardEpisode: "{Series Title} - S{season:00}E{episode:00} - {Episode Title}",
			SlotDailyEpisode:    "{Series Title} - {Air-Date} - {Episode Title}",
			SlotAnimeEpisode:    "{Series Title} - S{season:00}E{episode:00} - {Episode Title}",
			SlotSeriesFolder:    "{Series TitleYear} [[tvdbid-{TvdbId}]]",
			SlotSeasonFolder:    "Season {season:00}",
			SlotSpecialsFolder:  "Specials",
		}},
	},
}

// Naming is one library's naming, resolved: templates, character options, whether files are renamed at all, and how
// one file covering several episodes writes its numbers.
type Naming struct {
	Formats      map[string]string
	Options      naming.Options
	RenameFiles  bool
	MultiEpisode MultiEpisodeStyle
}

// FormatMultiEpisodeStyle is the one option a TV naming profile stores beside its templates, under the same map.
const FormatMultiEpisodeStyle = "multi_episode_style"

// NamingFor resolves a library's stored profile; nil is the scheme's default preset.
func NamingFor(profile *settings.NamingProfile) Naming {
	if profile == nil {
		return Naming{Formats: NamingScheme.Default().Formats, Options: naming.DefaultOptions, RenameFiles: true, MultiEpisode: StylePrefixedRange}
	}
	style := MultiEpisodeStyle(profile.Formats[FormatMultiEpisodeStyle])
	if !style.Valid() {
		style = StylePrefixedRange
	}
	return Naming{
		Formats: profile.Formats,
		Options: naming.Options{
			ReplaceIllegalCharacters: profile.ReplaceIllegalCharacters,
			ColonReplacement:         naming.ColonReplacement(profile.ColonReplacement),
		},
		RenameFiles:  profile.RenameFiles,
		MultiEpisode: style,
	}
}

// DefaultNamingProfile is NamingFor(nil) in stored form: what a never-saved library uses.
func DefaultNamingProfile() settings.NamingProfile {
	formats := maps.Clone(NamingScheme.Default().Formats)
	formats[FormatMultiEpisodeStyle] = string(StylePrefixedRange)
	return settings.NamingProfile{
		RenameFiles: true, ReplaceIllegalCharacters: naming.DefaultOptions.ReplaceIllegalCharacters,
		ColonReplacement: string(naming.DefaultOptions.ColonReplacement), Formats: formats,
	}
}

// ValidateNaming is settings.NamingValidator for TV.
func ValidateNaming(profile settings.NamingProfile) error {
	templates := maps.Clone(profile.Formats)
	style := MultiEpisodeStyle(templates[FormatMultiEpisodeStyle])
	delete(templates, FormatMultiEpisodeStyle)
	problems := []error{NamingScheme.Validate(templates)}
	if style != "" && !style.Valid() {
		problems = append(problems, fmt.Errorf("naming: unknown multi-episode style %q", style))
	}
	if !naming.ColonReplacement(profile.ColonReplacement).Valid() {
		problems = append(problems, fmt.Errorf("naming: unknown colon replacement %q", profile.ColonReplacement))
	}
	return errors.Join(problems...)
}

// SeriesType decides which episode format a series uses.
type SeriesType string

// The three types a series can have (Sonarr's series type).
const (
	SeriesStandard SeriesType = "standard"
	SeriesDaily    SeriesType = "daily"
	SeriesAnime    SeriesType = "anime"
)

// EpisodeNumbering is one episode a file covers.
type EpisodeNumbering struct {
	Number   int
	Absolute int
	Title    string
}

// EpisodeNaming is every fact an episode file's name can draw on; a zero field renders as absent.
type EpisodeNaming struct {
	SeriesType       SeriesType
	SeriesTitle      string
	SeriesYear       int
	IDs              IDs
	Season           int
	Episodes         []EpisodeNumbering
	AirDate          string
	QualityTitle     string
	Proper           bool
	VideoCodec       string
	DynamicRange     string
	AudioCodec       string
	AudioChannels    string
	AudioLanguages   string
	SubtitleLangs    string
	ReleaseGroup     string
	Version          int
	OriginalTitle    string
	OriginalFilename string
}

// RenderEpisode renders one file's path below its root: series folder, season (or specials) folder, then the episode
// format of the series type. With RenameFiles off the file keeps originalRelPath's own name.
func (n Naming) RenderEpisode(episode EpisodeNaming, originalRelPath string) (string, []naming.Warning, error) {
	slot := SlotStandardEpisode
	switch episode.SeriesType {
	case SeriesDaily:
		slot = SlotDailyEpisode
	case SeriesAnime:
		slot = SlotAnimeEpisode
	}
	folder := SlotSeasonFolder
	if episode.Season == 0 {
		folder = SlotSpecialsFolder
	}
	fileFormat := n.expandSeasonEpisode(n.Formats[slot], episode)
	segments, err := naming.Compile(n.Formats[SlotSeriesFolder] + "/" + n.Formats[folder] + "/" + fileFormat)
	if err != nil {
		return "", nil, fmt.Errorf("tv: naming %s: %w", slot, err)
	}
	rendered, warnings, err := naming.RenderPath(segments, episode.Context(), n.Options, path.Ext(originalRelPath))
	if err != nil {
		return "", warnings, err
	}
	if !n.RenameFiles {
		rendered = path.Join(path.Dir(rendered), path.Base(originalRelPath))
	}
	return rendered, warnings, nil
}

// RenderSeriesFolder renders the series folder alone, for creating an empty one.
func (n Naming) RenderSeriesFolder(episode EpisodeNaming) (string, error) {
	segments, err := naming.Compile(n.Formats[SlotSeriesFolder])
	if err != nil {
		return "", fmt.Errorf("tv: naming %s: %w", SlotSeriesFolder, err)
	}
	rendered, _, err := naming.RenderPath(segments, episode.Context(), n.Options, "")
	return rendered, err
}

// seasonEpisodeBlock is the "S{season:00}E{episode:00}" run a multi-episode style rewrites (Sonarr's own pattern).
var seasonEpisodeBlock = regexp.MustCompile(`(?i)(s?)\{season(?::(0+))?\}([-_. ]?[ex])\{episode(?::(0+))?\}([-_. ]*)`)

var trailingPart = regexp.MustCompile(`\s*\(\d+\)$`)

// expandSeasonEpisode rewrites the season-and-episode block for a file covering several episodes; one episode keeps
// the template as written.
func (n Naming) expandSeasonEpisode(format string, episode EpisodeNaming) string {
	if len(episode.Episodes) < 2 {
		return format
	}
	return seasonEpisodeBlock.ReplaceAllStringFunc(format, func(block string) string {
		parts := seasonEpisodeBlock.FindStringSubmatch(block)
		prefix, seasonWidth, separator, episodeWidth, after := parts[1], len(parts[2]), parts[3], len(parts[4]), parts[5]
		season := prefix + pad(episode.Season, seasonWidth)
		letter := strings.Trim(separator, "-_. ")
		numbers := make([]string, len(episode.Episodes))
		for i, e := range episode.Episodes {
			numbers[i] = pad(e.Number, episodeWidth)
		}
		first, last := numbers[0], numbers[len(numbers)-1]
		var text string
		switch n.MultiEpisode {
		case StyleExtend:
			text = season + separator + strings.Join(numbers, "-")
		case StyleDuplicate:
			joiner := after
			if joiner == "" {
				joiner = "."
			}
			each := make([]string, len(numbers))
			for i, number := range numbers {
				each[i] = season + separator + number
			}
			text = strings.Join(each, joiner)
		case StyleRepeat:
			text = season + separator + strings.Join(numbers, letter)
		case StyleScene:
			text = season + separator + strings.Join(numbers, "-"+letter)
		case StyleRange:
			text = season + separator + first + "-" + last
		default:
			text = season + separator + first + "-" + letter + last
		}
		return text + after
	})
}

func pad(n, width int) string {
	text := strconv.Itoa(n)
	for len(text) < width {
		text = "0" + text
	}
	return text
}

// Context spells every token out, the derived forms included.
func (e EpisodeNaming) Context() naming.Context {
	ctx := naming.Context{}
	set := func(name, value string) {
		if value != "" {
			ctx[name] = value
		}
	}
	if e.SeriesTitle != "" {
		set(TokenSeriesTitle, e.SeriesTitle)
		set(TokenSeriesTitleThe, naming.TitleThe(e.SeriesTitle))
		set(TokenSeriesCleanTitle, naming.CleanTitle(e.SeriesTitle))
		set(TokenSeriesCleanTitleThe, naming.TitleThe(naming.CleanTitle(e.SeriesTitle)))
		withYear := e.SeriesTitle
		theYear := naming.TitleThe(e.SeriesTitle)
		if e.SeriesYear > 0 && !endsWithYear(e.SeriesTitle) {
			withYear = fmt.Sprintf("%s (%d)", e.SeriesTitle, e.SeriesYear)
			theYear = fmt.Sprintf("%s (%d)", naming.TitleThe(e.SeriesTitle), e.SeriesYear)
		}
		set(TokenSeriesTitleYear, withYear)
		set(TokenSeriesTitleTheYear, theYear)
	}
	if e.SeriesYear > 0 {
		set(TokenSeriesYear, strconv.Itoa(e.SeriesYear))
	}
	set(TokenSeason, strconv.Itoa(e.Season))
	if len(e.Episodes) > 0 {
		set(TokenEpisode, strconv.Itoa(e.Episodes[0].Number))
		if e.Episodes[0].Absolute > 0 {
			set(TokenAbsolute, strconv.Itoa(e.Episodes[0].Absolute))
		}
		title := episodeTitle(e.Episodes)
		set(TokenEpisodeTitle, title)
		set(TokenEpisodeCleanTitle, naming.CleanTitle(title))
	}
	if len(e.AirDate) == 10 {
		set(TokenAirDate, e.AirDate)
		set(TokenAirDateSpaced, strings.ReplaceAll(e.AirDate, "-", " "))
	}
	set(TokenQualityTitle, e.QualityTitle)
	if e.QualityTitle != "" {
		full := e.QualityTitle
		if e.Proper {
			full += " Proper"
		}
		set(TokenQualityFull, full)
	}
	set(TokenVideoCodec, e.VideoCodec)
	set(TokenVideoDynamicRange, e.DynamicRange)
	set(TokenAudioCodec, e.AudioCodec)
	set(TokenAudioChannels, e.AudioChannels)
	set(TokenAudioLanguages, e.AudioLanguages)
	set(TokenSubtitleLanguages, e.SubtitleLangs)
	set(TokenReleaseGroup, e.ReleaseGroup)
	if e.Version > 1 {
		set(TokenRevisionVersion, "v"+strconv.Itoa(e.Version))
	}
	set(TokenOriginalTitle, e.OriginalTitle)
	set(TokenOriginalFilename, e.OriginalFilename)
	if e.IDs.TVDB > 0 {
		set(TokenTvdbID, strconv.FormatInt(e.IDs.TVDB, 10))
	}
	if e.IDs.TMDB > 0 {
		set(TokenTmdbID, strconv.FormatInt(e.IDs.TMDB, 10))
	}
	set(TokenImdbID, e.IDs.IMDb)
	if e.IDs.TVmaze > 0 {
		set(TokenTvMazeID, strconv.FormatInt(e.IDs.TVmaze, 10))
	}
	return ctx
}

var yearSuffix = regexp.MustCompile(`\(\d{4}\)$`)

func endsWithYear(title string) bool { return yearSuffix.MatchString(strings.TrimSpace(title)) }

// episodeTitle is one title for the episodes a file covers: several titles that differ only by a trailing (1), (2)
// are one title, any others are joined with " + " (Sonarr's rule).
func episodeTitle(episodes []EpisodeNumbering) string {
	titles := make([]string, 0, len(episodes))
	for _, e := range episodes {
		if strings.TrimSpace(e.Title) != "" {
			titles = append(titles, strings.TrimSpace(e.Title))
		}
	}
	if len(titles) == 0 {
		return ""
	}
	base := make([]string, len(titles))
	allSame := true
	for i, title := range titles {
		base[i] = trailingPart.ReplaceAllString(title, "")
		if base[i] != base[0] {
			allSame = false
		}
	}
	if allSame {
		return base[0]
	}
	return strings.Join(titles, " + ")
}

// NamingExample is one rendered sample for the naming screen.
type NamingExample struct {
	Label string
	Path  string
}

// NamingExamples renders the samples the naming screen shows under its formats.
func NamingExamples(n Naming) []NamingExample {
	ids := IDs{TVDB: 12345, TMDB: 67890, IMDb: "tt1234567", TVmaze: 321}
	base := EpisodeNaming{
		SeriesTitle: "The Series Title's!", SeriesYear: 2010, IDs: ids, Season: 1, QualityTitle: "WEBDL-1080p", VideoCodec: "x264",
		AudioCodec: "EAC3", AudioChannels: "5.1", ReleaseGroup: "GROUP", OriginalFilename: "series.title.s01e01.720p",
	}
	single, multi, daily, anime, special := base, base, base, base, base
	single.Episodes = []EpisodeNumbering{{Number: 1, Absolute: 1, Title: "Episode's Title"}}
	multi.Episodes = []EpisodeNumbering{{Number: 1, Title: "Episode's Title 1"}, {Number: 2, Title: "Episode's Title 2"}, {Number: 3, Title: "Episode's Title 3"}}
	daily.SeriesType, daily.AirDate, daily.Season = SeriesDaily, "2016-03-20", 2016
	daily.Episodes = single.Episodes
	anime.SeriesType = SeriesAnime
	anime.Episodes = []EpisodeNumbering{{Number: 1, Absolute: 12, Title: "Episode's Title"}}
	special.Season, special.Episodes = 0, []EpisodeNumbering{{Number: 1, Title: "Special's Title"}}

	var examples []NamingExample
	for _, sample := range []struct {
		label   string
		episode EpisodeNaming
	}{
		{"Single Episode Example", single}, {"Multi-Episode Example", multi}, {"Daily Episode Example", daily},
		{"Anime Episode Example", anime}, {"Specials Example", special},
	} {
		rendered, _, err := n.RenderEpisode(sample.episode, "series.title.s01e01.720p.mkv")
		if err != nil {
			rendered = err.Error()
		}
		examples = append(examples, NamingExample{Label: sample.label, Path: rendered})
	}
	return examples
}
