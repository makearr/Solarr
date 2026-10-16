package movie

import (
	"fmt"
	"path"
	"strconv"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// Naming tokens, Radarr's spelling (ADR-23: the movie vocabulary, never core's).
const (
	TokenMovieTitle        = "Movie Title"
	TokenMovieCleanTitle   = "Movie CleanTitle"
	TokenMovieTitleThe     = "Movie TitleThe"
	TokenMovieCleanThe     = "Movie CleanTitleThe"
	TokenMovieFirst        = "Movie TitleFirstCharacter"
	TokenMovieOriginal     = "Movie OriginalTitle"
	TokenMovieCertificate  = "Movie Certification"
	TokenMovieCollection   = "Movie Collection"
	TokenMovieCollectionTh = "Movie CollectionThe"
	TokenReleaseYear       = "Release Year"
	TokenEditionTags       = "Edition Tags"
	TokenQualityFull       = "Quality Full"
	TokenQualityTitle      = "Quality Title"
	TokenVideoCodec        = "MediaInfo VideoCodec"
	TokenVideoDynamicRange = "MediaInfo VideoDynamicRangeType"
	TokenAudioCodec        = "MediaInfo AudioCodec"
	TokenAudioChannels     = "MediaInfo AudioChannels"
	TokenAudioLanguages    = "MediaInfo AudioLanguages"
	TokenSubtitleLanguages = "MediaInfo SubtitleLanguages"
	TokenReleaseGroup      = "Release Group"
	TokenRevisionVersion   = "Revision Version"
	TokenOriginalTitle     = "Original Title"
	TokenOriginalFilename  = "Original Filename"
	TokenImdbID            = "ImdbId"
	TokenTmdbID            = "TmdbId"
)

// Naming slots, Radarr's two formats.
const (
	SlotStandardMovie = "standard_movie"
	SlotMovieFolder   = "movie_folder"
)

// NamingScheme is the movie naming declaration. The first preset names the way Radarr's own default names a library.
var NamingScheme = naming.Scheme{
	Slots: []naming.Slot{
		{Key: SlotStandardMovie, Label: "Standard Movie Format", Help: "The file name of a movie. A / starts a folder."},
		{Key: SlotMovieFolder, Label: "Movie Folder Format", Help: "The folder each movie goes in."},
	},
	Tokens: []naming.Token{
		{Spelling: "{Movie Title}", Group: "Movie", Example: "The Movie Title's!", Help: "The movie's title."},
		{Spelling: "{Movie CleanTitle}", Group: "Movie", Example: "The Movie Titles", Help: "The title without punctuation, & written as and."},
		{Spelling: "{Movie TitleThe}", Group: "Movie", Example: "Movie Title's!, The", Help: "The title with a leading The, A or An moved to the end."},
		{Spelling: "{Movie CleanTitleThe}", Group: "Movie", Example: "Movie Titles, The", Help: "The clean title with its leading article moved to the end."},
		{Spelling: "{Movie TitleFirstCharacter}", Group: "Movie", Example: "M", Help: "The first letter or digit of the title, for a folder per letter."},
		{Spelling: "{Movie OriginalTitle}", Group: "Movie", Example: "Le Titre", Help: "The title in the movie's original language."},
		{Spelling: "{Movie Certification}", Group: "Movie", Example: "R", Help: "The age rating."},
		{Spelling: "{Movie Collection}", Group: "Movie", Example: "The Movie Collection", Help: "The franchise the movie belongs to."},
		{Spelling: "{Movie CollectionThe}", Group: "Movie", Example: "Movie Collection, The", Help: "The franchise with its article moved to the end."},
		{Spelling: "{Release Year}", Group: "Movie", Example: "2010", Help: "The year the movie was released."},
		{Spelling: "{Edition Tags}", Group: "Movie", Example: "Director's Cut", Help: "The cut or edition the file is, when it is one."},
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
		{Spelling: "{Original Title}", Group: "Original", Example: "Movie.Title.2010.1080p.BluRay-GROUP", Help: "The release name the file arrived with."},
		{Spelling: "{Original Filename}", Group: "Original", Example: "movie.title.2010.1080p", Help: "The name the file arrived with, without its extension."},
		{Spelling: "{ImdbId}", Group: "Ids", Example: "tt1234567", Help: "The movie's IMDb id."},
		{Spelling: "{TmdbId}", Group: "Ids", Example: "12345", Help: "The movie's TMDB id."},
	},
	Presets: []naming.Preset{
		{Name: "Solarr default", Formats: map[string]string{
			SlotStandardMovie: "{Movie Title} ({Release Year})",
			SlotMovieFolder:   "{Movie Title} ({Release Year})",
		}},
		{Name: "Radarr default", Formats: map[string]string{
			SlotStandardMovie: "{Movie Title} ({Release Year}) {Quality Full}",
			SlotMovieFolder:   "{Movie Title} ({Release Year})",
		}},
		{Name: "Plex", Formats: map[string]string{
			SlotStandardMovie: "{Movie Title} ({Release Year})[ {{edition-{Edition Tags}}}]",
			SlotMovieFolder:   "{Movie Title} ({Release Year}) {{imdb-{ImdbId}}}",
		}},
		{Name: "Jellyfin", Formats: map[string]string{
			SlotStandardMovie: "{Movie Title} ({Release Year})[ - {Edition Tags}]",
			SlotMovieFolder:   "{Movie Title} ({Release Year}) [[tmdbid-{TmdbId}]]",
		}},
	},
}

// Naming is one library's naming, resolved: templates, character options and whether files are renamed at all.
type Naming struct {
	Formats     map[string]string
	Options     naming.Options
	RenameFiles bool
}

// defaultOptions delete a colon, as the library this default reproduces was named ("Digvus: Vutg" is "Digvus Vutg").
var defaultOptions = naming.Options{ReplaceIllegalCharacters: true, ColonReplacement: naming.ColonDelete}

// NamingFor resolves a library's stored profile; nil is the scheme's default preset.
func NamingFor(profile *settings.NamingProfile) Naming {
	if profile == nil {
		return Naming{Formats: NamingScheme.Default().Formats, Options: defaultOptions, RenameFiles: true}
	}
	return Naming{
		Formats: profile.Formats,
		Options: naming.Options{
			ReplaceIllegalCharacters: profile.ReplaceIllegalCharacters,
			ColonReplacement:         naming.ColonReplacement(profile.ColonReplacement),
		},
		RenameFiles: profile.RenameFiles,
	}
}

// DefaultNamingProfile is NamingFor(nil) in stored form: what a never-saved library uses.
func DefaultNamingProfile() settings.NamingProfile {
	return settings.NamingProfile{
		RenameFiles: true, ReplaceIllegalCharacters: defaultOptions.ReplaceIllegalCharacters,
		ColonReplacement: string(defaultOptions.ColonReplacement), Formats: NamingScheme.Default().Formats,
	}
}

// ValidateNaming is settings.NamingValidator for movies.
func ValidateNaming(profile settings.NamingProfile) error {
	if err := NamingScheme.Validate(profile.Formats); err != nil {
		return err
	}
	if !naming.ColonReplacement(profile.ColonReplacement).Valid() {
		return fmt.Errorf("naming: unknown colon replacement %q", profile.ColonReplacement)
	}
	return nil
}

// FileNaming is every fact a movie file's name can draw on; a zero field renders as absent.
type FileNaming struct {
	Title            string
	OriginalTitle    string
	Year             int
	IDs              IDs
	Certification    string
	Collection       string
	Edition          string
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
	OriginalTitleRel string
	OriginalFilename string
}

// RenderMovie renders one file's path below its root: the movie folder, then the file format. With RenameFiles off the
// file keeps originalRelPath's own name.
func (n Naming) RenderMovie(movie FileNaming, originalRelPath string) (string, []naming.Warning, error) {
	segments, err := naming.Compile(n.Formats[SlotMovieFolder] + "/" + n.Formats[SlotStandardMovie])
	if err != nil {
		return "", nil, fmt.Errorf("movie: naming: %w", err)
	}
	rendered, warnings, err := naming.RenderPath(segments, movie.Context(), n.Options, path.Ext(originalRelPath))
	if err != nil {
		return "", warnings, err
	}
	if !n.RenameFiles {
		rendered = path.Join(path.Dir(rendered), path.Base(originalRelPath))
	}
	return rendered, warnings, nil
}

// Context spells every token out, the derived forms included.
func (m FileNaming) Context() naming.Context {
	ctx := naming.Context{}
	set := func(name, value string) {
		if value != "" {
			ctx[name] = value
		}
	}
	if m.Title != "" {
		set(TokenMovieTitle, m.Title)
		set(TokenMovieCleanTitle, naming.CleanTitle(m.Title))
		set(TokenMovieTitleThe, naming.TitleThe(m.Title))
		set(TokenMovieCleanThe, naming.TitleThe(naming.CleanTitle(m.Title)))
		set(TokenMovieFirst, naming.FirstCharacter(m.Title))
	}
	set(TokenMovieOriginal, m.OriginalTitle)
	set(TokenMovieCertificate, m.Certification)
	if m.Collection != "" {
		set(TokenMovieCollection, m.Collection)
		set(TokenMovieCollectionTh, naming.TitleThe(m.Collection))
	}
	if m.Year > 0 {
		set(TokenReleaseYear, strconv.Itoa(m.Year))
	}
	set(TokenEditionTags, m.Edition)
	set(TokenQualityTitle, m.QualityTitle)
	if m.QualityTitle != "" {
		full := m.QualityTitle
		if m.Proper {
			full += " Proper"
		}
		set(TokenQualityFull, full)
	}
	set(TokenVideoCodec, m.VideoCodec)
	set(TokenVideoDynamicRange, m.DynamicRange)
	set(TokenAudioCodec, m.AudioCodec)
	set(TokenAudioChannels, m.AudioChannels)
	set(TokenAudioLanguages, m.AudioLanguages)
	set(TokenSubtitleLanguages, m.SubtitleLangs)
	set(TokenReleaseGroup, m.ReleaseGroup)
	if m.Version > 1 {
		set(TokenRevisionVersion, "v"+strconv.Itoa(m.Version))
	}
	set(TokenOriginalTitle, m.OriginalTitleRel)
	set(TokenOriginalFilename, m.OriginalFilename)
	set(TokenImdbID, m.IDs.IMDb)
	if m.IDs.TMDB > 0 {
		set(TokenTmdbID, strconv.FormatInt(m.IDs.TMDB, 10))
	}
	return ctx
}

// NamingExample is one rendered sample for the naming screen.
type NamingExample = media.NamingExample

// NamingExamples renders the samples the naming screen shows under its formats.
func NamingExamples(n Naming) []NamingExample {
	base := FileNaming{
		Title: "The Movie Title's!", OriginalTitle: "Le Titre", Year: 2010, IDs: IDs{TMDB: 12345, IMDb: "tt1234567"}, Certification: "R",
		Collection: "The Movie Collection", QualityTitle: "WEBDL-1080p", VideoCodec: "x264", AudioCodec: "EAC3", AudioChannels: "5.1",
		ReleaseGroup: "GROUP", OriginalFilename: "movie.title.2010.1080p",
	}
	cut := base
	cut.Edition = "Director's Cut"
	var examples []NamingExample
	for _, sample := range []struct {
		label string
		movie FileNaming
	}{{"Movie Example", base}, {"Edition Example", cut}} {
		rendered, _, err := n.RenderMovie(sample.movie, "movie.title.2010.1080p.mkv")
		if err != nil {
			rendered = err.Error()
		}
		examples = append(examples, NamingExample{Label: sample.label, Path: rendered})
	}
	return examples
}
