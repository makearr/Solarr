package comic

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// Provider keys under which a series' ids are stored; AniList and MyAnimeList are kept apart from their anime ids.
const (
	ProviderComicVine    = "comicvine"
	ProviderMetron       = "metron"
	ProviderGCD          = "gcd"
	ProviderAniListManga = "anilist:manga"
	ProviderMAL          = "mal:manga"
	ProviderMangaUpdates = "mangaupdates"
	ProviderMangaDex     = "mangadex"
)

// Part kinds, the first half of a part's media_ref.
const (
	PartIssue   = "issue"
	PartAnnual  = "annual"
	PartSpecial = "special"
	PartVolume  = "volume"
	PartChapter = "chapter"
)

// Format keys of a series (Kapowarr's "special version"), stored as the field comic_format.
const (
	FormatSeries        = "series"
	FormatLimited       = "limited"
	FormatOneShot       = "one_shot"
	FormatAnnual        = "annual"
	FormatTradePaperbck = "trade_paperback"
	FormatHardcover     = "hardcover"
	FormatGraphicNovel  = "graphic_novel"
	FormatOmnibus       = "omnibus"
	FormatGallery       = "gallery"
)

// AltSchemeChapter is the alternate numbering of a manga volume part: the chapters the provider lists in it.
const AltSchemeChapter = "chapter"

// FieldFormat is the stored field a series' format is read from.
const FieldFormat = "comic_format"

// Series is a series merged over the providers that know it, as the library stores it.
type Series struct {
	Title, Overview, Publisher, Format, Status, OriginalLanguage, Cover string
	Year, EndYear                                                       int
	// Adult is what a provider says: AniList's isAdult, MangaDex's erotica and pornographic.
	Adult bool
	// Manga says the series is read by volume, right to left.
	Manga bool
	// IDs are the series' ids by provider key.
	IDs     map[string]string
	Aliases []string
	Genres  []string
	Credits []Credit
	Parts   []Part
	// Sources are the providers that answered, in the order they supplied fields.
	Sources []string
	// Failed are the providers that hold the series' id and did not answer.
	Failed []string
}

// Credit is one person's role on a series; Order ranks the lead first.
type Credit struct {
	Name, Role string
	Order      int
}

// Part is one issue, annual, volume or chapter of a series.
type Part struct {
	Kind string
	// Number is as printed: "1", "1.5", "0", "1AU", "-1".
	Number                  string
	Title, CoverDate, Store string
	Pages                   int
	Rating, Cover           string
	// IDs are the part's ids by provider key.
	IDs map[string]string
	// Chapters are the chapter numbers a manga volume holds, when a provider lists them.
	Chapters []string
}

// primaryKind is the kind of part a series is read by: volumes for manga, issues for comics.
func (s Series) primaryKind() string {
	if s.Manga {
		return PartVolume
	}
	return PartIssue
}

// Ref is the part's media_ref.
func (p Part) Ref() string { return p.Kind + ":" + p.Number }

// defaultTitle names a part its provider gave no title: "Issue #1", "Volume 3", "Chapter 12".
func (p Part) defaultTitle() string {
	switch p.Kind {
	case PartVolume:
		return "Volume " + p.Number
	case PartChapter:
		return "Chapter " + p.Number
	case PartAnnual:
		return "Annual #" + p.Number
	case PartSpecial:
		return "Special #" + p.Number
	}
	return "Issue #" + p.Number
}

// Date is the day the part is out: the store date, else the cover date.
func (p Part) Date() (day, basis string) { return p.dateBy(false) }

// dateBy is Date, cover date first when the library's date type is the cover date.
func (p Part) dateBy(coverFirst bool) (day, basis string) {
	if coverFirst {
		if day = normaliseDay(p.CoverDate); day != "" {
			return day, "cover date"
		}
	}
	if day = normaliseDay(p.Store); day != "" {
		return day, "store date"
	}
	if day = normaliseDay(p.CoverDate); day != "" {
		return day, "cover date"
	}
	return "", ""
}

// normaliseDay reads "2020-03-00" and "2020-03" as the first of the month and drops what is no date.
func normaliseDay(text string) string {
	parts := strings.Split(strings.TrimSpace(text), "-")
	if len(parts) < 2 || len(parts[0]) != 4 {
		return ""
	}
	if len(parts) == 2 || parts[2] == "00" || parts[2] == "" {
		parts = []string{parts[0], parts[1], "01"}
	}
	day := strings.Join(parts[:3], "-")
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return ""
	}
	return day
}

// kindOrder keeps issues and volumes first, annuals and specials after them and loose chapters last.
var kindOrder = map[string]int{PartIssue: 0, PartVolume: 0, PartAnnual: 1, PartSpecial: 2, PartChapter: 3}

// SortKey orders parts: kind, then the number's integer, then what follows it ("1", "1.5", "1AU" in that order).
func SortKey(kind, number string) string {
	whole, rest := splitNumber(number)
	return fmt.Sprintf("%d:%09d:%s", kindOrder[kind], whole+100000000, rest)
}

// splitNumber cuts a printed number into its leading integer and the remainder.
func splitNumber(number string) (whole int, rest string) {
	number = strings.TrimSpace(number)
	end := 0
	if strings.HasPrefix(number, "-") {
		end = 1
	}
	for end < len(number) && number[end] >= '0' && number[end] <= '9' {
		end++
	}
	whole, _ = strconv.Atoi(number[:end])
	return whole, strings.ToLower(number[end:])
}

// MonitorOption is Sonarr's wording of what to monitor of a series.
type MonitorOption string

// The monitor options.
const (
	MonitorAll     MonitorOption = "all"
	MonitorFuture  MonitorOption = "future"
	MonitorMissing MonitorOption = "missing"
	MonitorFirst   MonitorOption = "first"
	MonitorLatest  MonitorOption = "latest"
	MonitorNone    MonitorOption = "none"
)

// Valid says the option is one the type offers.
func (o MonitorOption) Valid() bool {
	switch o {
	case MonitorAll, MonitorFuture, MonitorMissing, MonitorFirst, MonitorLatest, MonitorNone:
		return true
	}
	return false
}

// Monitored picks the parts an option wants. A chapter not yet in a volume is never wanted.
func Monitored(parts []Part, option MonitorOption, now time.Time) map[string]bool {
	wanted := make(map[string]bool, len(parts))
	var first, latest string
	for _, part := range parts {
		if part.Kind == PartChapter {
			continue
		}
		key := SortKey(part.Kind, part.Number)
		if part.Kind == PartIssue || part.Kind == PartVolume {
			if first == "" || key < first {
				first = key
			}
			if key > latest {
				latest = key
			}
		}
	}
	for _, part := range parts {
		ref := part.Ref()
		key := SortKey(part.Kind, part.Number)
		switch {
		case part.Kind == PartChapter:
			wanted[ref] = false
		case option == MonitorAll || option == MonitorMissing:
			wanted[ref] = true
		case option == MonitorFirst:
			wanted[ref] = key == first
		case option == MonitorLatest:
			wanted[ref] = key == latest
		case option == MonitorFuture:
			day, _ := part.Date()
			wanted[ref] = day == "" || day >= now.UTC().Format("2006-01-02")
		default:
			wanted[ref] = false
		}
	}
	return wanted
}

// WriteOptions are what a write needs besides the series.
type WriteOptions struct {
	Monitor MonitorOption
	Now     time.Time
	// NewPart, when set, decides the monitoring of each part a write creates instead of Monitor.
	NewPart func(Part) bool
	// CoverDate dates a part by its cover date where it has one, the library's date type.
	CoverDate bool
}

// ToWrite turns a merged series into what the library stores.
func ToWrite(series Series, options WriteOptions) library.SeriesWrite {
	var yearPtr *int64
	if series.Year > 0 {
		year := int64(series.Year)
		yearPtr = &year
	}
	source := ""
	if len(series.Sources) > 0 {
		source = series.Sources[0]
	}
	write := library.SeriesWrite{
		Work:             library.WorkIdentity{MediaType: MediaType, CanonicalTitle: series.Title, SortTitle: library.SortTitle(series.Title)},
		Year:             yearPtr,
		OriginalLanguage: series.OriginalLanguage,
		Description:      series.Overview,
		IDs:              idWrites(series.IDs),
		Aliases:          series.Aliases,
		AliasSource:      "comic-merge",
		Genres:           series.Genres,
		GenreSource:      source,
		GenreTree:        GenreTree.Key,
		Network:          series.Publisher,
		OrganisationKind: "publisher",
		CertificationAge: -1,
		Fields:           fieldWrites(series, source),
		LeadRole:         RoleWriter,
	}
	for _, credit := range series.Credits {
		write.Credits = append(write.Credits, library.SeriesCreditWrite{
			Provider: ProviderPerson, ExternalID: personKey(credit.Name), Name: credit.Name, Role: credit.Role, Ord: credit.Order,
		})
	}
	wants := Monitored(series.Parts, options.Monitor, options.Now)
	for _, part := range series.Parts {
		monitored := wants[part.Ref()]
		if options.NewPart != nil {
			monitored = options.NewPart(part)
		}
		write.Parts = append(write.Parts, partWrite(part, monitored, options.CoverDate))
	}
	return write
}

// ProviderPerson keys a credit by the person's name: the providers' own person ids differ and are not carried.
const ProviderPerson = "comic:person"

func personKey(name string) string { return strings.ToLower(strings.Join(strings.Fields(name), " ")) }

func partWrite(part Part, monitored, coverDate bool) library.SeriesPartWrite {
	write := library.SeriesPartWrite{
		MediaRef: part.Ref(), SortKey: SortKey(part.Kind, part.Number), Title: firstNonEmpty(part.Title, part.defaultTitle()), MonitoredIfNew: monitored,
	}
	if day, basis := part.dateBy(coverDate); day != "" {
		write.Date = day
		write.AvailableBasis = basis
		write.AvailableConfidence = "low"
		if at, err := time.Parse("2006-01-02", day); err == nil {
			unix := at.Unix()
			write.AvailableAt = &unix
		}
	} else {
		write.AvailableBasis = library.AvailabilityUnannounced
	}
	for _, chapter := range part.Chapters {
		write.AltNumbers = append(write.AltNumbers, library.AltNumberWrite{Scheme: AltSchemeChapter, Provider: ProviderMangaDex, Ref: chapter})
	}
	if len(part.Chapters) > 0 {
		write.ReplaceAltProviders = []string{ProviderMangaDex}
	}
	for provider, id := range part.IDs {
		write.ExternalIDs = append(write.ExternalIDs, library.ExternalIDWrite{Provider: provider + ":part", ExternalID: id})
	}
	return write
}

func idWrites(ids map[string]string) []library.ExternalIDWrite {
	var writes []library.ExternalIDWrite
	for _, provider := range []string{ProviderComicVine, ProviderMetron, ProviderGCD, ProviderAniListManga, ProviderMAL, ProviderMangaUpdates, ProviderMangaDex} {
		if id := ids[provider]; id != "" {
			writes = append(writes, library.ExternalIDWrite{Provider: provider, ExternalID: id})
		}
	}
	return writes
}

func fieldWrites(series Series, source string) []library.SeriesFieldWrite {
	var fields []library.SeriesFieldWrite
	add := func(field, value string) {
		if strings.TrimSpace(value) != "" {
			fields = append(fields, library.SeriesFieldWrite{Field: field, Provider: source, Value: value})
		}
	}
	add("status", series.Status)
	add("publisher", series.Publisher)
	add(FieldFormat, series.Format)
	if series.Adult {
		add("adult", "true")
	}
	if series.EndYear > 0 {
		add("end_year", strconv.Itoa(series.EndYear))
	}
	add("cover", series.Cover)
	return fields
}

// isMangaKind says the library kind reads by volume: manga and hentai.
func isMangaKind(kind string) bool { return kind == KindManga || kind == KindHentai }

// isAdultKind says the library kind is one of the two adult ones.
func isAdultKind(kind string) bool { return kind == KindAdultComics || kind == KindHentai }
