package comic

import (
	"fmt"
	"path"
	"strconv"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// Naming tokens of a comic file.
const (
	TokenSeriesTitle      = "Series Title"
	TokenSeriesCleanTitle = "Series CleanTitle"
	TokenSeriesYear       = "Year"
	TokenPublisher        = "Publisher"
	TokenIssue            = "Issue"
	TokenIssueTitle       = "Issue Title"
	TokenVolume           = "Volume"
	TokenChapter          = "Chapter"
	TokenFormat           = "Format"
	TokenReleaseYear      = "Release Year"
	TokenQuality          = "Quality"
	TokenReleaseGroup     = "Release Group"
	TokenOriginalFilename = "Original Filename"
	TokenComicVineID      = "ComicVine Id"
	TokenMetronID         = "Metron Id"
)

// Naming slots: the series folder and the file of an issue, a manga volume and a manga chapter.
const (
	SlotSeriesFolder = "series_folder"
	SlotIssueFile    = "issue_file"
	SlotVolumeFile   = "volume_file"
	SlotChapterFile  = "chapter_file"
)

func tokens() []naming.Token {
	return []naming.Token{
		{Spelling: "{Series Title}", Group: "Series", Example: "Night Harbor", Help: "The series' title."},
		{Spelling: "{Series CleanTitle}", Group: "Series", Example: "Night Harbor", Help: "The title without punctuation, & written as and."},
		{Spelling: "{Year}", Group: "Series", Example: "2019", Help: "The year the series began."},
		{Spelling: "{Publisher}", Group: "Series", Example: "Acme Press", Help: "The series' publisher."},
		{Spelling: "{Issue}", Group: "Issue", Example: "3", Help: "The issue's number as printed; {Issue:000} pads its whole part, so 1.5 becomes 001.5 and 1AU becomes 001AU."},
		{Spelling: "{Issue Title}", Group: "Issue", Example: "Dawn", Help: "The issue's own title, when it has one."},
		{Spelling: "{Volume}", Group: "Volume", Example: "2", Help: "A manga volume's number, or a collected edition's volume; {Volume:00} pads it."},
		{Spelling: "{Chapter}", Group: "Volume", Example: "12.5", Help: "A manga chapter's number; {Chapter:000} pads its whole part."},
		{Spelling: "{Format}", Group: "Series", Example: "TPB", Help: "The series' format when it is not an ordinary run: TPB, HC, GN, Omnibus, One-Shot."},
		{Spelling: "{Release Year}", Group: "Issue", Example: "2019", Help: "The year the issue went on sale or carries on its cover, as the library's date type says."},
		{Spelling: "{Quality}", Group: "Release", Example: "CBZ", Help: "The file's format."},
		{Spelling: "{Release Group}", Group: "Release", Example: "GROUP", Help: "The release group the name credits."},
		{Spelling: "{Original Filename}", Group: "Release", Example: "night_harbor_003", Help: "The name the file arrived with, without its extension."},
		{Spelling: "{ComicVine Id}", Group: "Ids", Example: "4050-1001", Help: "The series' ComicVine id."},
		{Spelling: "{Metron Id}", Group: "Ids", Example: "55", Help: "The series' Metron id."},
	}
}

// NamingScheme is the comic naming declaration; the first preset is Solarr's default, the others reproduce what Mylar, Issuarr and Kapowarr write.
func NamingScheme() naming.Scheme {
	slots := []naming.Slot{
		{Key: SlotSeriesFolder, Label: "Series Folder Format", Help: "The folder each series' files go in."},
		{Key: SlotIssueFile, Label: "Issue File Format", Help: "The file name of an issue, below the series folder."},
		{Key: SlotVolumeFile, Label: "Manga Volume File Format", Help: "The file name of a manga volume."},
		{Key: SlotChapterFile, Label: "Manga Chapter File Format", Help: "The file name of a manga chapter that is not in a volume yet."},
	}
	presets := []naming.Preset{
		{Name: "Solarr default", Formats: map[string]string{
			SlotSeriesFolder: "{Series Title} ({Year})",
			SlotIssueFile:    "{Series Title} ({Year}) #{Issue:000}",
			SlotVolumeFile:   "{Series Title} v{Volume:00}",
			SlotChapterFile:  "{Series Title} c{Chapter:000}",
		}},
		{Name: "Mylar and Issuarr", Formats: map[string]string{
			SlotSeriesFolder: "{Series Title} ({Year})",
			SlotIssueFile:    "{Series Title} {Issue:000} ({Release Year})",
			SlotVolumeFile:   "{Series Title} v{Volume:00} ({Release Year})",
			SlotChapterFile:  "{Series Title} c{Chapter:000}",
		}},
		{Name: "Kapowarr", Formats: map[string]string{
			SlotSeriesFolder: "{Series Title}/Volume {Volume:00} ({Year})",
			SlotIssueFile:    "{Series Title} ({Year}) Volume {Volume:00} Issue {Issue:000}",
			SlotVolumeFile:   "{Series Title} ({Year}) Volume {Volume:00}",
			SlotChapterFile:  "{Series Title} ({Year}) Chapter {Chapter:000}",
		}},
	}
	return naming.Scheme{Slots: slots, Tokens: tokens(), Presets: presets}
}

// Naming is one library's naming, resolved.
type Naming struct {
	Formats     map[string]string
	Options     naming.Options
	RenameFiles bool
}

var defaultOptions = naming.Options{ReplaceIllegalCharacters: true, ColonReplacement: naming.ColonDelete}

// NamingFor resolves a library's stored profile; nil is the scheme's default preset.
func NamingFor(profile *settings.NamingProfile) Naming {
	return Naming(NamingScheme().Resolve(profile, defaultOptions))
}

// NamingForKind is NamingFor, with a manga library's own default: a series folder without a year, as its readers expect.
func NamingForKind(profile *settings.NamingProfile, kind string) Naming {
	names := NamingFor(profile)
	if profile == nil && isMangaKind(kind) {
		names.Formats[SlotSeriesFolder] = "{Series Title}"
	}
	return names
}

// FileNaming is every fact a comic file's name can draw on; a zero field renders as absent.
type FileNaming struct {
	Series, Publisher, IssueTitle string
	// Year is the year the series began; ReleaseYear the issue's own.
	Year, ReleaseYear int
	// Issue and Chapter are as printed ("1", "1.5", "1AU"); Volume is 0 when the file has none.
	Issue, Chapter string
	Volume         int
	// VolumeLast is the last volume of a file holding a range; 0 or Volume for one volume.
	VolumeLast int
	// Format is the series' format key (tpb, hc, gn, omnibus, one-shot...); an ordinary run leaves it empty.
	Format                                  string
	Quality, ReleaseGroup, OriginalFilename string
	ComicVineID, MetronID                   string
}

// Context spells every token out, the derived forms included.
func (f FileNaming) Context() naming.Context {
	ctx := naming.Context{}
	set := func(name, value string) {
		if value != "" {
			ctx[name] = value
		}
	}
	if f.Series != "" {
		set(TokenSeriesTitle, f.Series)
		set(TokenSeriesCleanTitle, naming.CleanTitle(f.Series))
	}
	if f.Year > 0 {
		set(TokenSeriesYear, strconv.Itoa(f.Year))
	}
	if f.ReleaseYear > 0 {
		set(TokenReleaseYear, strconv.Itoa(f.ReleaseYear))
	}
	set(TokenPublisher, f.Publisher)
	set(TokenIssue, f.Issue)
	set(TokenIssueTitle, f.IssueTitle)
	if f.Volume > 0 {
		printed := strconv.Itoa(f.Volume)
		if f.VolumeLast > f.Volume {
			printed += "-" + strconv.Itoa(f.VolumeLast)
		}
		set(TokenVolume, printed)
	}
	set(TokenChapter, f.Chapter)
	set(TokenFormat, formatLabels[f.Format])
	set(TokenQuality, f.Quality)
	set(TokenReleaseGroup, f.ReleaseGroup)
	set(TokenOriginalFilename, f.OriginalFilename)
	set(TokenComicVineID, f.ComicVineID)
	set(TokenMetronID, f.MetronID)
	return ctx
}

var formatLabels = map[string]string{"tpb": "TPB", "hc": "HC", "gn": "GN", "omnibus": "Omnibus", "one-shot": "One-Shot", "annual": "Annual", "special": "Special"}

// RenderFile renders one file's path below its root: the series folder, then the slot's file format. With RenameFiles off the file keeps originalRelPath's own name.
func (n Naming) RenderFile(slot string, file FileNaming, originalRelPath string) (string, []naming.Warning, error) {
	format, ok := n.Formats[slot]
	if !ok {
		return "", nil, fmt.Errorf("comic: naming: no format for slot %q", slot)
	}
	segments, err := naming.Compile(n.Formats[SlotSeriesFolder] + "/" + format)
	if err != nil {
		return "", nil, fmt.Errorf("comic: naming: %w", err)
	}
	rendered, warnings, err := naming.RenderPath(segments, file.Context(), n.Options, path.Ext(originalRelPath))
	if err != nil {
		return "", warnings, err
	}
	if !n.RenameFiles {
		rendered = path.Join(path.Dir(rendered), path.Base(originalRelPath))
	}
	return rendered, warnings, nil
}

// NamingExamples renders the samples the naming screen shows under its formats.
func NamingExamples(n Naming) []media.NamingExample {
	base := FileNaming{Series: "Night Harbor", Publisher: "Acme Press", Year: 2019, ReleaseYear: 2019, Issue: "3", IssueTitle: "Dawn", Volume: 2, Chapter: "12", Quality: "CBZ", OriginalFilename: "night_harbor_003"}
	decimal, annual := base, base
	decimal.Issue = "1.5"
	annual.Issue, annual.Format = "1AU", "annual"
	var examples []media.NamingExample
	for _, sample := range []struct {
		label, slot string
		file        FileNaming
	}{
		{"Issue Example", SlotIssueFile, base}, {"Decimal Issue Example", SlotIssueFile, decimal}, {"Suffixed Issue Example", SlotIssueFile, annual},
		{"Manga Volume Example", SlotVolumeFile, base}, {"Manga Chapter Example", SlotChapterFile, base},
	} {
		rendered, _, err := n.RenderFile(sample.slot, sample.file, "original.cbz")
		if err != nil {
			rendered = err.Error()
		}
		examples = append(examples, media.NamingExample{Label: sample.label, Path: rendered})
	}
	return examples
}
