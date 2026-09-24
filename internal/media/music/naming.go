package music

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// Naming tokens, Lidarr's spelling (ADR-23: music's vocabulary, never core's).
const (
	TokenArtistName               = "Artist Name"
	TokenArtistNameThe            = "Artist NameThe"
	TokenArtistCleanName          = "Artist CleanName"
	TokenArtistCleanNameThe       = "Artist CleanNameThe"
	TokenArtistNameFirstCharacter = "Artist NameFirstCharacter"
	TokenAlbumTitle               = "Album Title"
	TokenAlbumTitleThe            = "Album TitleThe"
	TokenAlbumCleanTitle          = "Album CleanTitle"
	TokenAlbumCleanTitleThe       = "Album CleanTitleThe"
	TokenAlbumType                = "Album Type"
	TokenAlbumGenre               = "Album Genre"
	TokenAlbumMbID                = "Album MbId"
	TokenReleaseYear              = "Release Year"
	TokenMedium                   = "medium"
	TokenMediumFormat             = "Medium Format"
	TokenTrack                    = "track"
	TokenTrackTitle               = "Track Title"
	TokenTrackCleanTitle          = "Track CleanTitle"
	TokenAudioCodec               = "MediaInfo AudioCodec"
	TokenAudioBitRate             = "MediaInfo AudioBitRate"
	TokenOriginalFilename         = "Original Filename"
)

// Naming slots, Lidarr's three formats.
const (
	SlotArtistFolder   = "artist_folder"
	SlotStandardTrack  = "standard_track"
	SlotMultiDiscTrack = "multi_disc_track"
)

// NamingScheme is music's naming declaration. The first preset is the user's own default
// (2026-09-19), which reproduces the test library byte for byte.
var NamingScheme = naming.Scheme{
	Slots: []naming.Slot{
		{Key: SlotArtistFolder, Label: "Artist Folder Format", Help: "The folder each artist's albums go in."},
		{Key: SlotStandardTrack, Label: "Standard Track Format",
			Help: "A track of a single-disc release, below the artist folder. A / starts a folder."},
		{Key: SlotMultiDiscTrack, Label: "Multi Disc Track Format",
			Help: "A track of a release with more than one disc."},
	},
	Tokens: []naming.Token{
		{Spelling: "{Artist Name}", Group: "Artist", Example: "The Artist Name", Help: "The artist's name."},
		{Spelling: "{Artist NameThe}", Group: "Artist", Example: "Artist Name, The", Help: "The name with a leading The, A or An moved to the end."},
		{Spelling: "{Artist CleanName}", Group: "Artist", Example: "The Artist Name", Help: "The name without punctuation, & written as and."},
		{Spelling: "{Artist CleanNameThe}", Group: "Artist", Example: "Artist Name, The", Help: "The clean name with its leading article moved to the end."},
		{Spelling: "{Artist NameFirstCharacter}", Group: "Artist", Example: "A", Help: "The first letter of the name, ignoring a leading article. For A-Z folders."},
		{Spelling: "{Album Title}", Group: "Album", Example: "The Album Title", Help: "The album's title."},
		{Spelling: "{Album TitleThe}", Group: "Album", Example: "Album Title, The", Help: "The title with a leading The, A or An moved to the end."},
		{Spelling: "{Album CleanTitle}", Group: "Album", Example: "The Album Title", Help: "The title without punctuation, & written as and."},
		{Spelling: "{Album CleanTitleThe}", Group: "Album", Example: "Album Title, The", Help: "The clean title with its leading article moved to the end."},
		{Spelling: "{Album Type}", Group: "Album", Example: "Album", Help: "Album, EP, Single, Broadcast or Other, from MusicBrainz."},
		{Spelling: "{Album Genre}", Group: "Album", Example: "Rock", Help: "The album's first genre."},
		{Spelling: "{Album MbId}", Group: "Album", Example: "d3f7a7c6-…", Help: "The MusicBrainz release group id."},
		{Spelling: "{Release Year}", Group: "Release date", Example: "2016", Help: "The year this release came out."},
		{Spelling: "{medium:0}", Group: "Medium", Example: "1", Help: "The disc number."},
		{Spelling: "{medium:00}", Group: "Medium", Example: "01", Help: "The disc number, two digits."},
		{Spelling: "{Medium Format}", Group: "Medium", Example: "CD", Help: "The medium: CD, Vinyl, Digital Media and so on."},
		{Spelling: "{track:0}", Group: "Track", Example: "1", Help: "The track number on its disc."},
		{Spelling: "{track:00}", Group: "Track", Example: "01", Help: "The track number, two digits."},
		{Spelling: "{Track Title}", Group: "Track", Example: "Track Title", Help: "The track's title."},
		{Spelling: "{Track CleanTitle}", Group: "Track", Example: "Track Title", Help: "The title without punctuation, & written as and."},
		{Spelling: "{MediaInfo AudioCodec}", Group: "Media info", Example: "FLAC", Help: "The audio codec measured from the file."},
		{Spelling: "{MediaInfo AudioBitRate}", Group: "Media info", Example: "320 kbps", Help: "The bitrate measured from the file."},
		{Spelling: "{Original Filename}", Group: "Original", Example: "01 - track title", Help: "The name the file arrived with, without its extension."},
	},
	Presets: []naming.Preset{
		{Name: "Solarr default", Formats: map[string]string{
			SlotArtistFolder:   "{Artist Name}",
			SlotStandardTrack:  "{Album Title} ({Release Year})/{Artist Name} - {track:00} - {Track Title}",
			SlotMultiDiscTrack: "{Album Title} ({Release Year})/{Artist Name} - {medium:0}:{track:00} - {Track Title}",
		}},
		{Name: "Lidarr default", Formats: map[string]string{
			SlotArtistFolder:   "{Artist Name}",
			SlotStandardTrack:  "{Album Title} ({Release Year})/{Artist Name} - {Album Title} - {track:00} - {Track Title}",
			SlotMultiDiscTrack: "{Album Title} ({Release Year})/{Medium Format} {medium:00}/{Artist Name} - {Album Title} - {track:00} - {Track Title}",
		}},
		{Name: "Disc folders", Formats: map[string]string{
			SlotArtistFolder:   "{Artist Name}",
			SlotStandardTrack:  "{Album Title} ({Release Year})/{track:00} - {Track Title}",
			SlotMultiDiscTrack: "{Album Title} ({Release Year})/Disc {medium:0}/{track:00} - {Track Title}",
		}},
	},
}

// Naming is one library's naming, resolved: templates, character options, and whether files
// are renamed at all (Lidarr's "Rename Tracks").
type Naming struct {
	Formats     map[string]string
	Options     naming.Options
	RenameFiles bool
}

// NamingFor resolves a library's stored profile; nil is the scheme's default preset.
func NamingFor(profile *settings.NamingProfile) Naming {
	if profile == nil {
		return Naming{Formats: NamingScheme.Default().Formats, Options: naming.DefaultOptions, RenameFiles: true}
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
		RenameFiles: true, ReplaceIllegalCharacters: naming.DefaultOptions.ReplaceIllegalCharacters,
		ColonReplacement: string(naming.DefaultOptions.ColonReplacement),
		Formats:          NamingScheme.Default().Formats,
	}
}

// ValidateNaming is settings.NamingValidator for music.
func ValidateNaming(profile settings.NamingProfile) error {
	problems := []error{NamingScheme.Validate(profile.Formats)}
	if !naming.ColonReplacement(profile.ColonReplacement).Valid() {
		problems = append(problems, fmt.Errorf("naming: unknown colon replacement %q", profile.ColonReplacement))
	}
	return errors.Join(problems...)
}

// RenderTrack renders one track's path below its root: the artist folder, then the standard
// or multi-disc format. With RenameFiles off the file keeps originalRelPath's own name.
func (n Naming) RenderTrack(track TrackNaming, discCount int, originalRelPath string) (string, []naming.Warning, error) {
	slot := SlotStandardTrack
	if discCount > 1 {
		slot = SlotMultiDiscTrack
	}
	segments, err := naming.Compile(n.Formats[SlotArtistFolder] + "/" + n.Formats[slot])
	if err != nil {
		return "", nil, fmt.Errorf("music: naming %s: %w", slot, err)
	}
	extension := path.Ext(originalRelPath)
	rendered, warnings, err := naming.RenderPath(segments, track.Context(), n.Options, extension)
	if err != nil {
		return "", warnings, err
	}
	if !n.RenameFiles {
		rendered = keptFileName(rendered, originalRelPath)
	}
	return rendered, warnings, nil
}

// TrackNaming is every fact a track's name can draw on; a zero field renders as absent.
type TrackNaming struct {
	ArtistName       string
	AlbumTitle       string
	AlbumType        string
	AlbumGenre       string
	AlbumMbID        string
	ReleaseYear      int
	Medium           int
	MediumFormat     string
	Track            int
	TrackTitle       string
	AudioCodec       string
	AudioBitRateKbps int64
	OriginalFilename string
}

// Context spells every token out, the derived forms included.
func (t TrackNaming) Context() naming.Context {
	ctx := naming.Context{}
	set := func(name, value string) {
		if value != "" {
			ctx[name] = value
		}
	}
	if t.ArtistName != "" {
		set(TokenArtistName, t.ArtistName)
		set(TokenArtistNameThe, naming.TitleThe(t.ArtistName))
		set(TokenArtistCleanName, naming.CleanTitle(t.ArtistName))
		set(TokenArtistCleanNameThe, naming.TitleThe(naming.CleanTitle(t.ArtistName)))
		set(TokenArtistNameFirstCharacter, naming.FirstCharacter(t.ArtistName))
	}
	if t.AlbumTitle != "" {
		set(TokenAlbumTitle, t.AlbumTitle)
		set(TokenAlbumTitleThe, naming.TitleThe(t.AlbumTitle))
		set(TokenAlbumCleanTitle, naming.CleanTitle(t.AlbumTitle))
		set(TokenAlbumCleanTitleThe, naming.TitleThe(naming.CleanTitle(t.AlbumTitle)))
	}
	set(TokenAlbumType, t.AlbumType)
	set(TokenAlbumGenre, t.AlbumGenre)
	set(TokenAlbumMbID, t.AlbumMbID)
	if t.ReleaseYear > 0 {
		set(TokenReleaseYear, strconv.Itoa(t.ReleaseYear))
	}
	// A file with no disc tag is on disc 1, as Lidarr numbers it.
	set(TokenMedium, strconv.Itoa(max(t.Medium, 1)))
	set(TokenMediumFormat, t.MediumFormat)
	if t.Track > 0 {
		set(TokenTrack, strconv.Itoa(t.Track))
	}
	if t.TrackTitle != "" {
		set(TokenTrackTitle, t.TrackTitle)
		set(TokenTrackCleanTitle, naming.CleanTitle(t.TrackTitle))
	}
	set(TokenAudioCodec, strings.ToUpper(t.AudioCodec))
	if t.AudioBitRateKbps > 0 {
		set(TokenAudioBitRate, fmt.Sprintf("%d kbps", t.AudioBitRateKbps))
	}
	set(TokenOriginalFilename, t.OriginalFilename)
	return ctx
}

// NamingExample is one rendered sample for the naming screen.
type NamingExample struct {
	Label string
	Path  string
}

var (
	singleDiscExample = TrackNaming{
		ArtistName: "Nick Cave & The Bad Seeds", AlbumTitle: "Tender Prey", AlbumType: "Album",
		AlbumGenre: "Rock", AlbumMbID: "58628005-ea1d-4199-8734-1dd41eaa12b6", ReleaseYear: 1988,
		Medium: 1, MediumFormat: "CD", Track: 1, TrackTitle: "The Furab Keke",
		AudioCodec: "flac", AudioBitRateKbps: 1011, OriginalFilename: "01 - the mercy seat",
	}
	multiDiscExample = TrackNaming{
		ArtistName: "Nick Cave & The Bad Seeds", AlbumTitle: "Abattoir Blues / The Lyre of Orpheus",
		AlbumType: "Album", AlbumGenre: "Rock", AlbumMbID: "cfc64505-10a0-4237-8828-42f165e388fd",
		ReleaseYear: 2004, Medium: 2, MediumFormat: "CD", Track: 3, TrackTitle: "Babe, You Turn Me On",
		AudioCodec: "flac", AudioBitRateKbps: 987, OriginalFilename: "2-03 babe you turn me on",
	}
)

// NamingExamples renders the samples the naming screen shows under its formats, Lidarr's
// "Single Track Example" and "Multi-Disc Track Example".
func NamingExamples(n Naming) []NamingExample {
	examples := make([]NamingExample, 0, 2)
	for _, sample := range []struct {
		label     string
		track     TrackNaming
		discCount int
	}{
		{"Single Track Example", singleDiscExample, 1},
		{"Multi-Disc Track Example", multiDiscExample, 2},
	} {
		rendered, _, err := n.RenderTrack(sample.track, sample.discCount, sample.track.OriginalFilename+".flac")
		if err != nil {
			rendered = err.Error()
		}
		examples = append(examples, NamingExample{Label: sample.label, Path: rendered})
	}
	return examples
}

// NamingContext builds a track's naming facts from the work detail page's own data.
func NamingContext(
	detail library.WorkDetail, edition library.EditionDetail, part library.PartDetail, currentRelPath string,
) (TrackNaming, error) {
	disc, track, err := parseMediaRef(part.Part.MediaRef)
	if err != nil {
		return TrackNaming{}, fmt.Errorf("music: naming context: %w", err)
	}
	facts := TrackNaming{
		AlbumTitle: detail.Work.CanonicalTitle, Medium: disc, Track: track,
		OriginalFilename: strings.TrimSuffix(path.Base(currentRelPath), path.Ext(currentRelPath)),
	}
	if artist, found := firstArtistCredit(detail.Credits); found {
		facts.ArtistName = artist
	}
	if year, ok := releaseYear(detail, edition); ok {
		facts.ReleaseYear = year
	}
	if detail.Work.PrimaryType.Valid {
		facts.AlbumType = detail.Work.PrimaryType.String
	}
	if len(detail.Genres) > 0 {
		facts.AlbumGenre = detail.Genres[0]
	}
	for _, id := range detail.ExternalIDs {
		if id.Provider == ProviderReleaseGroup {
			facts.AlbumMbID = id.ExternalID
			break
		}
	}
	if edition.Edition.Format.Valid {
		facts.MediumFormat = edition.Edition.Format.String
	}
	if part.Part.Title.Valid {
		facts.TrackTitle = part.Part.Title.String
	}
	if part.File != nil {
		facts.AudioCodec = part.File.AudioCodec
		facts.AudioBitRateKbps = part.File.BitrateKbps
	}
	return facts, nil
}

// mediaRefPattern parses music.Consumer's own "disc%d:track%d" convention back apart.
var mediaRefPattern = regexp.MustCompile(`^disc(\d+):track(\d+)$`)

// DiscCount is how many discs a release spans, counting only parts whose media_ref parses.
func DiscCount(parts []library.PartDetail) int {
	discs := make(map[int]struct{}, 1)
	for _, part := range parts {
		if disc, _, err := parseMediaRef(part.Part.MediaRef); err == nil {
			discs[disc] = struct{}{}
		}
	}
	return len(discs)
}

func firstArtistCredit(credits []library.Credit) (string, bool) {
	for _, credit := range credits {
		if credit.Role == CreditRoleArtist {
			return credit.PersonName, true
		}
	}
	return "", false
}

// releaseYear prefers the pressing's own year over the release group's first release:
// a reissue folder conventionally names the reissue.
func releaseYear(detail library.WorkDetail, edition library.EditionDetail) (int, bool) {
	if edition.Edition.Year.Valid {
		return int(edition.Edition.Year.Int64), true
	}
	if detail.Work.Year.Valid {
		return int(detail.Work.Year.Int64), true
	}
	return 0, false
}

func parseMediaRef(mediaRef string) (disc, track int, err error) {
	match := mediaRefPattern.FindStringSubmatch(mediaRef)
	if match == nil {
		return 0, 0, fmt.Errorf("media_ref %q does not match \"discN:trackM\"", mediaRef)
	}
	disc, _ = strconv.Atoi(match[1])
	track, _ = strconv.Atoi(match[2])
	return disc, track, nil
}
