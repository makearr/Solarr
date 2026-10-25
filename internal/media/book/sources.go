package book

import (
	"cmp"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/metadata/googlebooks"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// Source names, as listed in Book.Sources.
const (
	sourceOpenLibrary = "openlibrary"
	sourceGoogleBooks = "googlebooks"
	sourceAudible     = "audible"
	sourceWikidata    = "wikidata"
)

const (
	// maxGenres bounds the subjects kept: Open Library lists hundreds for a classic.
	maxGenres = 12
	maxAlias  = 10
)

// languageNames maps the language codes and words the providers use to ISO 639-1.
var languageNames = map[string]string{
	"eng": "en", "english": "en", "ger": "de", "deu": "de", "german": "de", "fre": "fr", "fra": "fr", "french": "fr",
	"spa": "es", "spanish": "es", "ita": "it", "italian": "it", "dut": "nl", "nld": "nl", "dutch": "nl", "por": "pt",
	"portuguese": "pt", "jpn": "ja", "japanese": "ja", "rus": "ru", "russian": "ru", "swe": "sv", "swedish": "sv",
	"pol": "pl", "polish": "pl", "chi": "zh", "zho": "zh", "chinese": "zh", "dan": "da", "danish": "da", "nor": "no", "norwegian": "no",
	"fin": "fi", "finnish": "fi",
}

// language is a provider's language as ISO 639-1, the code itself when it is already two letters.
func language(name string) string {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if code, known := languageNames[lowered]; known {
		return code
	}
	if len(lowered) == 2 {
		return lowered
	}
	return ""
}

// FromOpenLibrary turns Open Library's work and its search document into a book; authors name the work's writers with
// their Open Library ids, for their photos.
func FromOpenLibrary(work openlibrary.Work, doc openlibrary.Doc) Book {
	book := Book{
		IDs:   IDs{OpenLibrary: cmp.Or(strings.TrimPrefix(work.Key, "/works/"), doc.WorkID())},
		Title: cmp.Or(work.Title, doc.Title), Subtitle: cmp.Or(work.Subtitle, doc.Subtitle), Overview: cleanText(string(work.Description)),
		Year: doc.FirstPublishYear, Pages: doc.Pages, Sources: []string{sourceOpenLibrary},
	}
	if book.Year == 0 {
		book.Year = yearOf(work.FirstPublished)
	}
	if len(doc.Languages) > 0 {
		book.Language = language(doc.Languages[0])
	}
	if len(doc.Publishers) > 0 {
		book.Publisher = doc.Publishers[0]
	}
	if len(doc.Goodreads) > 0 {
		book.IDs.Goodreads = doc.Goodreads[0]
	}
	if len(doc.Google) > 0 {
		book.IDs.GoogleBooks = doc.Google[0]
	}
	book.IDs.ISBN = firstISBN13(doc.ISBN)
	subjects := work.Subjects
	if len(subjects) == 0 {
		subjects = doc.Subjects
	}
	book.Genres = genresOf(subjects)
	if doc.RatingsAverage > 0 {
		book.Ratings = []Rating{{Provider: sourceOpenLibrary, Score: doc.RatingsAverage * 2, Votes: doc.RatingsCount}}
	}
	covers := slices.Clone(work.Covers)
	if doc.CoverID > 0 && !slices.Contains(covers, doc.CoverID) {
		covers = append([]int64{doc.CoverID}, covers...)
	}
	for _, cover := range covers {
		if url := openlibrary.CoverURL(cover); url != "" && len(book.Images) < 6 {
			book.Images = append(book.Images, Image{Kind: "poster", URL: url, Source: sourceOpenLibrary})
		}
	}
	keys := work.AuthorIDs()
	if len(keys) == 0 {
		keys = doc.AuthorKeys
	}
	for index, name := range doc.AuthorNames {
		credit := Credit{Name: name, Role: RoleAuthor, Order: index}
		if index < len(keys) {
			credit.Image = openlibrary.AuthorPhotoURL(keys[index])
		}
		book.Credits = append(book.Credits, credit)
	}
	return book
}

// FromGoogleBooks turns a Google Books volume into a book.
func FromGoogleBooks(volume googlebooks.Volume) Book {
	info := volume.VolumeInfo
	book := Book{
		IDs:   IDs{GoogleBooks: volume.ID, ISBN: volume.ISBN()},
		Title: info.Title, Subtitle: info.Subtitle, Overview: cleanText(info.Description), Year: volume.Year(),
		Published: info.PublishedDate, Language: language(info.Language), Publisher: info.Publisher, Pages: info.PageCount,
		Genres: genresOf(info.Categories), Sources: []string{sourceGoogleBooks},
	}
	if len(book.IDs.ISBN) != 13 {
		book.IDs.ISBN = ""
	}
	if info.AverageRating > 0 {
		book.Ratings = []Rating{{Provider: sourceGoogleBooks, Score: info.AverageRating * 2, Votes: info.RatingsCount}}
	}
	if cover := volume.Cover(); cover != "" {
		book.Images = []Image{{Kind: "poster", URL: cover, Source: sourceGoogleBooks}}
	}
	for index, name := range info.Authors {
		book.Credits = append(book.Credits, Credit{Name: name, Role: RoleAuthor, Order: index})
	}
	return book
}

// FromAudible turns Audnexus's audiobook into a book: the reading, with its narrators, length and series.
func FromAudible(in audible.Book) Book {
	book := Book{
		IDs:   IDs{ASIN: in.ASIN},
		Title: in.Title, Subtitle: in.Subtitle, Overview: cleanText(cmp.Or(in.Summary, in.Description)), Year: in.Year(),
		Language: language(in.Language), Publisher: in.Publisher, RuntimeMin: in.RuntimeLengthMin, Abridged: in.Abridged(),
		Sources: []string{sourceAudible},
	}
	if len(in.ReleaseDate) >= 10 {
		book.Published = in.ReleaseDate[:10]
	}
	if isbn := strings.ReplaceAll(in.ISBN, "-", ""); len(isbn) == 13 {
		book.IDs.ISBN = isbn
	}
	for _, genre := range in.Genres {
		if genre.Type == "genre" && !slices.Contains(book.Genres, genre.Name) {
			book.Genres = append(book.Genres, genre.Name)
		}
	}
	if score, err := strconv.ParseFloat(in.Rating, 64); err == nil && score > 0 {
		book.Ratings = []Rating{{Provider: sourceAudible, Score: score * 2}}
	}
	if in.Image != "" {
		book.Images = []Image{{Kind: "poster", URL: in.Image, Source: sourceAudible}}
	}
	if in.SeriesPrimary != nil && in.SeriesPrimary.Name != "" {
		book.Series = &Series{Key: cmp.Or(in.SeriesPrimary.ASIN, in.SeriesPrimary.Name), Name: in.SeriesPrimary.Name, Position: cleanPosition(in.SeriesPrimary.Position)}
	}
	for index, author := range in.Authors {
		book.Credits = append(book.Credits, Credit{Name: author.Name, Role: RoleAuthor, Order: index})
	}
	for index, narrator := range in.Narrators {
		book.Credits = append(book.Credits, Credit{Name: narrator.Name, Role: RoleNarrator, Order: index})
	}
	return book
}

// FromWikidata turns what Wikidata's item says of a book into one: the Wikipedia lead, the first publication, its series
// and its writers, never a title.
func FromWikidata(in wikidata.WorkFacts) Book {
	book := Book{
		IDs:      IDs{OpenLibrary: in.IDs.OpenLibrary, GoogleBooks: in.IDs.GoogleBooks, ISBN: in.IDs.ISBN, Goodreads: in.IDs.Goodreads},
		Overview: in.Summary, Year: yearOf(in.Date), spareGenres: in.Genres, Sources: []string{sourceWikidata},
	}
	if in.OriginalTitle != "" {
		// The title in the language it was written in names folders and matches releases badly; it is another name.
		book.Aliases = []string{in.OriginalTitle}
	}
	if len(in.Languages) > 0 {
		book.Language = in.Languages[0]
	}
	if in.Series != nil {
		book.Series = &Series{Key: in.Series.QID, Name: in.Series.Name, Position: cleanPosition(in.Series.Position)}
	}
	for index, name := range in.Makers {
		book.Credits = append(book.Credits, Credit{Name: name, Role: RoleAuthor, Order: index})
	}
	return book
}

// Merge fills what the first book lacks from the second: the first is the preferred source, so a value it holds is
// never replaced. Lists it has stay its own, except ratings and pictures, which are the union, and credits, where a
// role it lacks (an audiobook's narrators) is taken from the second.
func Merge(first, second Book) Book {
	merged := first
	merged.IDs = IDs{
		OpenLibrary: cmp.Or(first.IDs.OpenLibrary, second.IDs.OpenLibrary), GoogleBooks: cmp.Or(first.IDs.GoogleBooks, second.IDs.GoogleBooks),
		ASIN: cmp.Or(first.IDs.ASIN, second.IDs.ASIN), ISBN: cmp.Or(first.IDs.ISBN, second.IDs.ISBN), Goodreads: cmp.Or(first.IDs.Goodreads, second.IDs.Goodreads),
	}
	merged.Title = cmp.Or(first.Title, second.Title)
	merged.Subtitle = cmp.Or(first.Subtitle, second.Subtitle)
	merged.Overview = cmp.Or(first.Overview, second.Overview)
	merged.Year = cmp.Or(first.Year, second.Year)
	merged.Published = cmp.Or(first.Published, second.Published)
	merged.Language = cmp.Or(first.Language, second.Language)
	merged.Publisher = cmp.Or(first.Publisher, second.Publisher)
	merged.Pages = cmp.Or(first.Pages, second.Pages)
	merged.RuntimeMin = cmp.Or(first.RuntimeMin, second.RuntimeMin)
	merged.Abridged = first.Abridged || second.Abridged
	if len(merged.Genres) == 0 {
		merged.Genres = second.Genres
	}
	if len(merged.spareGenres) == 0 {
		merged.spareGenres = second.spareGenres
	}
	if merged.Series == nil {
		merged.Series = second.Series
	} else if second.Series != nil && merged.Series.Position == "" && strings.EqualFold(merged.Series.Name, second.Series.Name) {
		series := *merged.Series
		series.Position = second.Series.Position
		merged.Series = &series
	}
	merged.Credits = mergeCredits(first.Credits, second.Credits)
	for _, rating := range second.Ratings {
		if !slices.ContainsFunc(merged.Ratings, func(have Rating) bool { return have.Provider == rating.Provider }) {
			merged.Ratings = append(slices.Clone(merged.Ratings), rating)
		}
	}
	for _, image := range second.Images {
		if !slices.ContainsFunc(merged.Images, func(have Image) bool { return have.URL == image.URL }) {
			merged.Images = append(slices.Clone(merged.Images), image)
		}
	}
	merged.Aliases = aliasesOf(merged.Title, append(slices.Clone(first.Aliases), append(slices.Clone(second.Aliases), second.Title)...))
	merged.Sources = append(slices.Clone(first.Sources), second.Sources...)
	return merged
}

// mergeCredits keeps the first's credits of each role it has, and takes the second's of a role it lacks; a picture the
// first lacks for someone the second names is taken too.
func mergeCredits(first, second []Credit) []Credit {
	merged := slices.Clone(first)
	for index, credit := range merged {
		if credit.Image != "" {
			continue
		}
		if at := slices.IndexFunc(second, func(other Credit) bool { return PersonKey(other.Name) == PersonKey(credit.Name) && other.Image != "" }); at >= 0 {
			merged[index].Image = second[at].Image
		}
	}
	for _, credit := range second {
		if !slices.ContainsFunc(first, func(have Credit) bool { return have.Role == credit.Role }) {
			merged = append(merged, credit)
		}
	}
	return merged
}

// aliasesOf is the book's other names, each once, never the title itself.
func aliasesOf(title string, names []string) []string {
	var aliases []string
	seen := map[string]bool{strings.ToLower(title): true}
	for _, name := range names {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" || seen[key] || len(aliases) >= maxAlias {
			continue
		}
		seen[key] = true
		aliases = append(aliases, name)
	}
	return aliases
}

// genreNoise are subjects Open Library files a book under that say nothing of its genre.
var genreNoise = regexp.MustCompile(`(?i)^(?:accessible book|protected daisy|in library|lending library|overdrive|nyt:|large type books|` +
	`open_syllabus_project|internet archive wishlist|reading level|fiction, general|general)`)

// genresOf keeps a provider's first subjects that read as genres: short, not a bookkeeping tag, "Fiction / Fantasy"
// taken as its last part.
func genresOf(subjects []string) []string {
	var genres []string
	for _, subject := range subjects {
		if parts := strings.Split(subject, "/"); len(parts) > 1 {
			subject = parts[len(parts)-1]
		}
		subject = strings.TrimSpace(subject)
		if subject == "" || len(subject) > 40 || genreNoise.MatchString(subject) || strings.ContainsAny(subject, "()=:") {
			continue
		}
		if !slices.ContainsFunc(genres, func(have string) bool { return strings.EqualFold(have, subject) }) {
			genres = append(genres, subject)
		}
		if len(genres) >= maxGenres {
			break
		}
	}
	return genres
}

var (
	tagPattern        = regexp.MustCompile(`<[^>]*>`)
	blankLinesPattern = regexp.MustCompile(`\n{3,}`)
	yearWordPattern   = regexp.MustCompile(`\b(1[5-9]|20)[0-9]{2}\b`)
)

// cleanText is a description without its HTML, as Audible and Google send it.
func cleanText(text string) string {
	text = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n\n").Replace(text)
	text = html.UnescapeString(tagPattern.ReplaceAllString(text, ""))
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSpace(line)
	}
	return strings.TrimSpace(blankLinesPattern.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

var positionPattern = regexp.MustCompile(`[0-9]*\.?[0-9]+`)

// cleanPosition is a place in a series as a number: Audible sends "Book 1" and "2, Dramatized Adaptation".
func cleanPosition(position string) string {
	if found := positionPattern.FindString(position); found != "" {
		return found
	}
	return strings.TrimSpace(position)
}

func firstISBN13(isbns []string) string {
	for _, isbn := range isbns {
		if len(isbn) == 13 && (strings.HasPrefix(isbn, "978") || strings.HasPrefix(isbn, "979")) {
			return isbn
		}
	}
	return ""
}

func yearOf(day string) int {
	if len(day) < 4 {
		return 0
	}
	year, err := strconv.Atoi(day[:4])
	if err != nil {
		// Open Library writes "May 1987" as often as "1987".
		if found := yearWordPattern.FindString(day); found != "" {
			year, _ = strconv.Atoi(found)
		}
	}
	return year
}
