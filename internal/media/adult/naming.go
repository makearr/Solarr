package adult

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// Naming tokens, Whisparr's spelling where it has one (ADR-23: the adult vocabulary, never core's).
const (
	TokenStudioName       = "Studio Name"
	TokenStudioCleanName  = "Studio CleanName"
	TokenStudioNameThe    = "Studio NameThe"
	TokenStudioFirst      = "Studio NameFirstCharacter"
	TokenNetworkName      = "Network Name"
	TokenSceneTitle       = "Scene Title"
	TokenSceneCleanTitle  = "Scene CleanTitle"
	TokenSceneTitleThe    = "Scene TitleThe"
	TokenSceneCode        = "Scene Code"
	TokenReleaseDate      = "Release Date"
	TokenReleaseYear      = "Release Year"
	TokenPerformerNames   = "Performer Names"
	TokenFirstPerformer   = "Performer FirstName"
	TokenReleaseGroup     = "Release Group"
	TokenOriginalFilename = "Original Filename"
	TokenStashID          = "StashDbId"
	TokenPartNumber       = "PartNumber"
)

// Naming slots: the studio's folder and the scene's file below it.
const (
	SlotStandardScene = "standard_scene"
	SlotStudioFolder  = "studio_folder"
)

// maxNamedPerformers bounds {Performer Names}: a file name has a length limit.
const maxNamedPerformers = 3

// NamingScheme is the adult naming declaration. The first preset is Solarr's default, the layout Stash users keep
// scenes in; Whisparr's follows.
var NamingScheme = naming.Scheme{
	Slots: []naming.Slot{
		{Key: SlotStudioFolder, Label: "Studio Folder Format", Help: "The folder each studio's scenes go in."},
		{Key: SlotStandardScene, Label: "Standard Scene Format", Help: "The file name of a scene, below its studio's folder. A / starts a folder."},
	},
	Tokens: []naming.Token{
		{Spelling: "{Studio Name}", Group: "Studio", Example: "Night Studio", Help: "The studio or site the scene is from."},
		{Spelling: "{Studio CleanName}", Group: "Studio", Example: "Night Studio", Help: "The studio without punctuation."},
		{Spelling: "{Studio NameThe}", Group: "Studio", Example: "Night Studio, The", Help: "The studio with a leading The moved to the end."},
		{Spelling: "{Studio NameFirstCharacter}", Group: "Studio", Example: "N", Help: "The first letter of the studio, for a folder per letter."},
		{Spelling: "{Network Name}", Group: "Studio", Example: "Night Network", Help: "The network the studio belongs to."},
		{Spelling: "{Scene Title}", Group: "Scene", Example: "Class Two", Help: "The scene's title."},
		{Spelling: "{Scene CleanTitle}", Group: "Scene", Example: "Class Two", Help: "The title without punctuation, & written as and."},
		{Spelling: "{Scene TitleThe}", Group: "Scene", Example: "Class Two, The", Help: "The title with its leading article moved to the end."},
		{Spelling: "{Scene Code}", Group: "Scene", Example: "E079", Help: "The studio's own code for the scene."},
		{Spelling: "{Release Date}", Group: "Scene", Example: "2020-05-14", Help: "The day the scene came out."},
		{Spelling: "{Release Year}", Group: "Scene", Example: "2020", Help: "The year the scene came out."},
		{Spelling: "{Performer Names}", Group: "Performers", Example: "Ada Vale, Bo Lane", Help: "The scene's first three performers."},
		{Spelling: "{Performer FirstName}", Group: "Performers", Example: "Ada Vale", Help: "The scene's first performer."},
		{Spelling: "{Release Group}", Group: "Release", Example: "GROUP", Help: "The release group the name credits."},
		{Spelling: "{Original Filename}", Group: "Release", Example: "night.20.05.14.class.two", Help: "The name the file arrived with, without its extension."},
		{Spelling: "{StashDbId}", Group: "Ids", Example: "c573c939-3dae-4a5f-8dc7-2abafb37440e", Help: "The scene's StashDB id."},
		{Spelling: "{PartNumber}", Group: "File", Example: "2", Help: "The file's number when the scene is in several files."},
	},
	Presets: []naming.Preset{
		{Name: "Solarr default", Formats: map[string]string{
			SlotStudioFolder:  "{Studio Name}",
			SlotStandardScene: "{Scene Title}[ ({Release Date})]/[[{Studio Name}]] {Scene Title}[ ({Release Date})][ - {PartNumber:00}]",
		}},
		{Name: "Whisparr default", Formats: map[string]string{
			SlotStudioFolder:  "{Studio Name}",
			SlotStandardScene: "{Studio Name} - {Release Date} - {Scene Title}",
		}},
		{Name: "With performers", Formats: map[string]string{
			SlotStudioFolder:  "{Studio Name}",
			SlotStandardScene: "{Studio Name} - {Release Date} - {Performer Names} - {Scene Title}",
		}},
	},
}

// Naming is one library's naming, resolved: templates, character options and whether files are renamed at all.
type Naming struct {
	Formats     map[string]string
	Options     naming.Options
	RenameFiles bool
}

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

// DefaultNamingProfile is NamingFor(nil) in stored form.
func DefaultNamingProfile() settings.NamingProfile {
	return settings.NamingProfile{
		RenameFiles: true, ReplaceIllegalCharacters: defaultOptions.ReplaceIllegalCharacters,
		ColonReplacement: string(defaultOptions.ColonReplacement), Formats: NamingScheme.Default().Formats,
	}
}

// ValidateNaming is settings.NamingValidator for scenes.
func ValidateNaming(profile settings.NamingProfile) error {
	if err := NamingScheme.Validate(profile.Formats); err != nil {
		return err
	}
	if !naming.ColonReplacement(profile.ColonReplacement).Valid() {
		return fmt.Errorf("naming: unknown colon replacement %q", profile.ColonReplacement)
	}
	return nil
}

// FileNaming is every fact a scene file's name can draw on; a zero field renders as absent.
type FileNaming struct {
	Studio, Network, Title, Code string
	Date                         string
	Year                         int
	Performers                   []string
	PartNumber, PartCount        int
	ReleaseGroup, StashID        string
	OriginalFilename             string
}

// RenderScene renders one file's path below its root: the studio folder, then the scene format. With RenameFiles off
// the file keeps originalRelPath's own name.
func (n Naming) RenderScene(scene FileNaming, originalRelPath string) (string, []naming.Warning, error) {
	segments, err := naming.Compile(n.Formats[SlotStudioFolder] + "/" + n.Formats[SlotStandardScene])
	if err != nil {
		return "", nil, fmt.Errorf("adult: naming: %w", err)
	}
	rendered, warnings, err := naming.RenderPath(segments, scene.Context(), n.Options, path.Ext(originalRelPath))
	if err != nil {
		return "", warnings, err
	}
	if !n.RenameFiles {
		rendered = path.Join(path.Dir(rendered), path.Base(originalRelPath))
	}
	return rendered, warnings, nil
}

// Context spells every token out, the derived forms included.
func (s FileNaming) Context() naming.Context {
	ctx := naming.Context{}
	set := func(name, value string) {
		if value != "" {
			ctx[name] = value
		}
	}
	// A scene whose studio is unknown goes in a folder that says so, as an unknown artist's does.
	studio := s.Studio
	if studio == "" {
		studio = "Unknown Studio"
	}
	set(TokenStudioName, studio)
	set(TokenStudioCleanName, naming.CleanTitle(studio))
	set(TokenStudioNameThe, naming.TitleThe(studio))
	set(TokenStudioFirst, naming.FirstCharacter(naming.TitleThe(studio)))
	set(TokenNetworkName, s.Network)
	if s.Title != "" {
		set(TokenSceneTitle, s.Title)
		set(TokenSceneCleanTitle, naming.CleanTitle(s.Title))
		set(TokenSceneTitleThe, naming.TitleThe(s.Title))
	}
	set(TokenSceneCode, s.Code)
	set(TokenReleaseDate, s.Date)
	set(TokenReleaseYear, yearString(s.Year))
	if len(s.Performers) > 0 {
		set(TokenFirstPerformer, s.Performers[0])
		set(TokenPerformerNames, strings.Join(s.Performers[:min(maxNamedPerformers, len(s.Performers))], ", "))
	}
	if s.PartCount > 1 && s.PartNumber > 0 {
		set(TokenPartNumber, strconv.Itoa(s.PartNumber))
	}
	set(TokenReleaseGroup, s.ReleaseGroup)
	set(TokenOriginalFilename, s.OriginalFilename)
	set(TokenStashID, s.StashID)
	return ctx
}

// NamingExamples renders the samples the naming screen shows under its formats.
func NamingExamples(n Naming) []media.NamingExample {
	base := FileNaming{
		Studio: "Night Studio", Network: "Night Network", Title: "Class Two", Code: "E079", Date: "2020-05-14", Year: 2020,
		Performers: []string{"Ada Vale", "Bo Lane"}, StashID: "c573c939-3dae-4a5f-8dc7-2abafb37440e", OriginalFilename: "night.20.05.14.class.two",
	}
	parted := base
	parted.PartNumber, parted.PartCount = 2, 2
	var examples []media.NamingExample
	for _, sample := range []struct {
		label string
		scene FileNaming
	}{{"Scene Example", base}, {"Multi-file Example", parted}} {
		rendered, _, err := n.RenderScene(sample.scene, "original.mp4")
		if err != nil {
			rendered = err.Error()
		}
		examples = append(examples, media.NamingExample{Label: sample.label, Path: rendered})
	}
	return examples
}
