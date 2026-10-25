package book

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// Naming tokens, Readarr's spelling (ADR-23: the book vocabulary, never core's).
const (
	TokenAuthorName       = "Author Name"
	TokenAuthorCleanName  = "Author CleanName"
	TokenAuthorNameThe    = "Author NameThe"
	TokenAuthorSortName   = "Author SortName"
	TokenAuthorFirst      = "Author NameFirstCharacter"
	TokenBookTitle        = "Book Title"
	TokenBookCleanTitle   = "Book CleanTitle"
	TokenBookTitleThe     = "Book TitleThe"
	TokenBookSubtitle     = "Book Subtitle"
	TokenBookSeries       = "Book Series"
	TokenBookSeriesPos    = "Book SeriesPosition"
	TokenBookSeriesTitle  = "Book SeriesTitle"
	TokenReleaseYear      = "Release Year"
	TokenNarratorName     = "Narrator Name"
	TokenPartNumber       = "PartNumber"
	TokenPartCount        = "PartCount"
	TokenReleaseGroup     = "Release Group"
	TokenOriginalFilename = "Original Filename"
	TokenISBN             = "ISBN"
	TokenASIN             = "ASIN"
)

// Naming slots, Readarr's two formats.
const (
	SlotStandardBook = "standard_book"
	SlotAuthorFolder = "author_folder"
)

func (f Format) tokens() []naming.Token {
	tokens := []naming.Token{
		{Spelling: "{Author Name}", Group: "Author", Example: "Mara Quell", Help: "The book's first author."},
		{Spelling: "{Author CleanName}", Group: "Author", Example: "Mara Quell", Help: "The author without punctuation."},
		{Spelling: "{Author NameThe}", Group: "Author", Example: "Quell Collective, The", Help: "The author with a leading The moved to the end."},
		{Spelling: "{Author SortName}", Group: "Author", Example: "Quell, Mara", Help: "The author surname first."},
		{Spelling: "{Author NameFirstCharacter}", Group: "Author", Example: "M", Help: "The first letter of the author, for a folder per letter."},
		{Spelling: "{Book Title}", Group: "Book", Example: "The Lantern Keeper", Help: "The book's title."},
		{Spelling: "{Book CleanTitle}", Group: "Book", Example: "The Lantern Keeper", Help: "The title without punctuation, & written as and."},
		{Spelling: "{Book TitleThe}", Group: "Book", Example: "Lantern Keeper, The", Help: "The title with its leading article moved to the end."},
		{Spelling: "{Book Subtitle}", Group: "Book", Example: "A Novel", Help: "The book's subtitle."},
		{Spelling: "{Book Series}", Group: "Book", Example: "The Lantern Cycle", Help: "The series the book belongs to."},
		{Spelling: "{Book SeriesPosition}", Group: "Book", Example: "1", Help: "The book's place in its series."},
		{Spelling: "{Book SeriesTitle}", Group: "Book", Example: "The Lantern Cycle #1", Help: "The series and the book's place in it."},
		{Spelling: "{Release Year}", Group: "Book", Example: "1987", Help: "The year the book was first published."},
		{Spelling: "{PartNumber}", Group: "File", Example: "1", Help: "The file's number when the book is in several files; {PartNumber:00} pads it."},
		{Spelling: "{PartCount}", Group: "File", Example: "12", Help: "How many files the book is in."},
		{Spelling: "{Release Group}", Group: "Release", Example: "GROUP", Help: "The release group the name credits."},
		{Spelling: "{Original Filename}", Group: "Release", Example: "the_lantern_keeper", Help: "The name the file arrived with, without its extension."},
		{Spelling: "{ISBN}", Group: "Ids", Example: "9780000000002", Help: "The book's ISBN-13."},
	}
	if f.ByFolder {
		tokens = append(tokens,
			naming.Token{Spelling: "{Narrator Name}", Group: "Author", Example: "Tobin Reyes", Help: "The reading's first narrator."},
			naming.Token{Spelling: "{ASIN}", Group: "Ids", Example: "B000000001", Help: "The audiobook's Audible ASIN."})
	}
	return tokens
}

// NamingScheme is the format's naming declaration. The first preset is Solarr's default; Readarr's and, for
// audiobooks, the layout Audiobookshelf reads follow.
func (f Format) NamingScheme() naming.Scheme {
	slots := []naming.Slot{
		{Key: SlotAuthorFolder, Label: "Author Folder Format", Help: "The folder each author's books go in."},
		{Key: SlotStandardBook, Label: "Standard Book Format", Help: "The file name of a book, below its author's folder. A / starts a folder."},
	}
	presets := []naming.Preset{
		{Name: "Solarr default", Formats: map[string]string{
			SlotAuthorFolder: "{Author Name}",
			SlotStandardBook: "{Book Title} ({Release Year})/{Author Name} - {Book Title}[ - {PartNumber:00}]",
		}},
		{Name: "Readarr default", Formats: map[string]string{
			SlotAuthorFolder: "{Author Name}",
			SlotStandardBook: "{Book Title}/{Author Name} - {Book Title}[ ({PartNumber})]",
		}},
	}
	if f.ByFolder {
		presets[0].Formats[SlotStandardBook] = "{Book Title} ({Release Year})/{Book Title}[ - {PartNumber:00}]"
		presets = append(presets, naming.Preset{Name: "Audiobookshelf", Formats: map[string]string{
			SlotAuthorFolder: "{Author Name}",
			SlotStandardBook: "[{Book SeriesPosition} - ]{Book Title}[ {{{Narrator Name}}}]/[{PartNumber:00} - ]{Book Title}",
		}})
	}
	return naming.Scheme{Slots: slots, Tokens: f.tokens(), Presets: presets}
}

// Naming is one library's naming, resolved: templates, character options and whether files are renamed at all.
type Naming struct {
	Formats     map[string]string
	Options     naming.Options
	RenameFiles bool
}

var defaultOptions = naming.Options{ReplaceIllegalCharacters: true, ColonReplacement: naming.ColonDelete}

// NamingFor resolves a library's stored profile; nil is the scheme's default preset.
func (f Format) NamingFor(profile *settings.NamingProfile) Naming {
	if profile == nil {
		return Naming{Formats: f.NamingScheme().Default().Formats, Options: defaultOptions, RenameFiles: true}
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
func (f Format) DefaultNamingProfile() settings.NamingProfile {
	return settings.NamingProfile{
		RenameFiles: true, ReplaceIllegalCharacters: defaultOptions.ReplaceIllegalCharacters,
		ColonReplacement: string(defaultOptions.ColonReplacement), Formats: f.NamingScheme().Default().Formats,
	}
}

// ValidateNaming is settings.NamingValidator for the format.
func (f Format) ValidateNaming(profile settings.NamingProfile) error {
	if err := f.NamingScheme().Validate(profile.Formats); err != nil {
		return err
	}
	if !naming.ColonReplacement(profile.ColonReplacement).Valid() {
		return fmt.Errorf("naming: unknown colon replacement %q", profile.ColonReplacement)
	}
	return nil
}

// FileNaming is every fact a book file's name can draw on; a zero field renders as absent.
type FileNaming struct {
	Author, Narrator         string
	Title, Subtitle          string
	Series, SeriesPosition   string
	Year                     int
	PartNumber, PartCount    int
	ReleaseGroup, ISBN, ASIN string
	OriginalFilename         string
}

// RenderBook renders one file's path below its root: the author folder, then the book format. With RenameFiles off the
// file keeps originalRelPath's own name.
func (n Naming) RenderBook(book FileNaming, originalRelPath string) (string, []naming.Warning, error) {
	segments, err := naming.Compile(n.Formats[SlotAuthorFolder] + "/" + n.Formats[SlotStandardBook])
	if err != nil {
		return "", nil, fmt.Errorf("book: naming: %w", err)
	}
	rendered, warnings, err := naming.RenderPath(segments, book.Context(), n.Options, path.Ext(originalRelPath))
	if err != nil {
		return "", warnings, err
	}
	if !n.RenameFiles {
		rendered = path.Join(path.Dir(rendered), path.Base(originalRelPath))
	}
	return rendered, warnings, nil
}

// Context spells every token out, the derived forms included.
func (b FileNaming) Context() naming.Context {
	ctx := naming.Context{}
	set := func(name, value string) {
		if value != "" {
			ctx[name] = value
		}
	}
	if b.Author != "" {
		set(TokenAuthorName, b.Author)
		set(TokenAuthorCleanName, naming.CleanTitle(b.Author))
		set(TokenAuthorNameThe, naming.TitleThe(b.Author))
		set(TokenAuthorSortName, sortName(b.Author))
		set(TokenAuthorFirst, naming.FirstCharacter(naming.TitleThe(b.Author)))
	}
	if b.Title != "" {
		set(TokenBookTitle, b.Title)
		set(TokenBookCleanTitle, naming.CleanTitle(b.Title))
		set(TokenBookTitleThe, naming.TitleThe(b.Title))
	}
	set(TokenBookSubtitle, b.Subtitle)
	set(TokenBookSeries, b.Series)
	set(TokenBookSeriesPos, b.SeriesPosition)
	if b.Series != "" && b.SeriesPosition != "" {
		set(TokenBookSeriesTitle, b.Series+" #"+b.SeriesPosition)
	} else {
		set(TokenBookSeriesTitle, b.Series)
	}
	if b.Year > 0 {
		set(TokenReleaseYear, strconv.Itoa(b.Year))
	}
	set(TokenNarratorName, b.Narrator)
	// A book in one file has no part: "[ - {PartNumber:00}]" then renders nothing.
	if b.PartCount > 1 && b.PartNumber > 0 {
		set(TokenPartNumber, strconv.Itoa(b.PartNumber))
		set(TokenPartCount, strconv.Itoa(b.PartCount))
	}
	set(TokenReleaseGroup, b.ReleaseGroup)
	set(TokenOriginalFilename, b.OriginalFilename)
	set(TokenISBN, b.ISBN)
	set(TokenASIN, b.ASIN)
	return ctx
}

// sortName is "Surname, Given names", Readarr's NameLastFirst.
func sortName(name string) string {
	words := strings.Fields(name)
	if len(words) < 2 {
		return name
	}
	return words[len(words)-1] + ", " + strings.Join(words[:len(words)-1], " ")
}

// NamingExamples renders the samples the naming screen shows under its formats.
func (f Format) NamingExamples(n Naming) []media.NamingExample {
	base := FileNaming{
		Author: "Mara Quell", Narrator: "Tobin Reyes", Title: "The Lantern Keeper", Subtitle: "A Novel", Series: "The Lantern Cycle",
		SeriesPosition: "1", Year: 1987, ISBN: "9780000000002", ASIN: "B000000001", OriginalFilename: "the_lantern_keeper",
	}
	extension := ".epub"
	if f.ByFolder {
		extension = ".m4b"
	}
	parted := base
	parted.PartNumber, parted.PartCount = 3, 12
	var examples []media.NamingExample
	for _, sample := range []struct {
		label string
		book  FileNaming
	}{{f.One + " Example", base}, {"Multi-file Example", parted}} {
		rendered, _, err := n.RenderBook(sample.book, "original"+extension)
		if err != nil {
			rendered = err.Error()
		}
		examples = append(examples, media.NamingExample{Label: sample.label, Path: rendered})
	}
	return examples
}
