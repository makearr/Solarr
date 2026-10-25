package wikidata

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// FactKind is what a work is, and so which id Wikidata finds it by.
type FactKind string

// The works facts are read for, by the id property that names them: TMDB's film id, TheTVDB's series id, MusicBrainz's
// release group id.
const (
	FactFilm   FactKind = "P4947"
	FactSeries FactKind = "P4835"
	FactAlbum  FactKind = "P436"
	// FactBook is Open Library's work id ("OL27482W"), which a literary work's item records.
	FactBook FactKind = "P648"
)

// WorkFacts are what Wikidata's item, and its English Wikipedia article, say of a film, a series or an album.
type WorkFacts struct {
	QID string
	// IDs are the ids other providers know the work by, as its item records them; QID is set when one item answered.
	IDs           WorkIDs
	OriginalTitle string
	// Date is the earliest publication (a series' start), "YYYY-MM-DD".
	Date       string
	RuntimeMin int
	Genres     []string
	// Languages are ISO 639-1 codes, Countries ISO 3166-1 alpha-2 codes.
	Languages, Countries []string
	// Labels are an album's record labels; Makers its performers, a film's directors, a series' creators.
	Labels, Makers []string
	// Summary is the English Wikipedia article's lead, empty without one.
	Summary string
	// Series is the series the work is part of (P179), with its place in it; nil when it names none.
	Series *SeriesPlace
}

// SeriesPlace is a work's series and the work's place in it ("3", "2.5"; empty when unnumbered).
type SeriesPlace struct {
	QID, Name, Position string
}

// factsPerQuery bounds the works one facts query asks about.
const factsPerQuery = 40

// FactsOf answers what Wikidata knows of each work, by the id its kind names it by; works it does not know are absent.
func (f Fetcher) FactsOf(ctx context.Context, kind FactKind, ids []string) (map[string]WorkFacts, error) {
	out := map[string]WorkFacts{}
	var wanted []string
	for _, id := range ids {
		if id != "" && !slices.Contains(wanted, id) {
			wanted = append(wanted, id)
		}
	}
	for start := 0; start < len(wanted); start += factsPerQuery {
		batch := wanted[start:min(start+factsPerQuery, len(wanted))]
		values := make([]string, len(batch))
		for index, id := range batch {
			values[index] = strconv.Quote(id)
		}
		query := `SELECT ?id ?item (SAMPLE(?title) AS ?original) (MIN(?date) AS ?first) (SAMPLE(?runtime) AS ?minutes)
  (GROUP_CONCAT(DISTINCT ?genreLabel; separator="|") AS ?genres) (GROUP_CONCAT(DISTINCT ?language; separator="|") AS ?languages)
  (GROUP_CONCAT(DISTINCT ?country; separator="|") AS ?countries) (GROUP_CONCAT(DISTINCT ?labelName; separator="|") AS ?labels)
  (GROUP_CONCAT(DISTINCT ?makerName; separator="|") AS ?makers) (SAMPLE(?tvdb) AS ?tvdbID) (SAMPLE(?tmdbSeries) AS ?tmdbSeriesID)
  (SAMPLE(?tmdbFilm) AS ?tmdbFilmID) (SAMPLE(?imdb) AS ?imdbID) (SAMPLE(?tvmaze) AS ?tvmazeID)
  (GROUP_CONCAT(DISTINCT ?anilist; separator="|") AS ?anilistIDs) (SAMPLE(?openLibrary) AS ?openLibraryID)
  (SAMPLE(?goodreads) AS ?goodreadsID) (SAMPLE(?googleBooks) AS ?googleBooksID) (SAMPLE(?isbn) AS ?isbnID)
  (SAMPLE(?seriesEntry) AS ?series) WHERE {
  VALUES ?id { ` + strings.Join(values, " ") + ` } ?item wdt:` + string(kind) + ` ?id .
  OPTIONAL { ?item wdt:P1476 ?title }
  OPTIONAL { ?item wdt:P577|wdt:P580 ?date }
  OPTIONAL { ?item wdt:P2047 ?runtime }
  OPTIONAL { ?item wdt:P136 ?genre . ?genre rdfs:label ?genreLabel . FILTER(LANG(?genreLabel) = "en") }
  OPTIONAL { ?item wdt:P364|wdt:P407 ?languageItem . ?languageItem wdt:P218 ?language }
  OPTIONAL { ?item wdt:P495 ?countryItem . ?countryItem wdt:P297 ?country }
  OPTIONAL { ?item wdt:P264 ?label . ?label rdfs:label ?labelName . FILTER(LANG(?labelName) = "en") }
  OPTIONAL { ?item wdt:P57|wdt:P170|wdt:P175|wdt:P50 ?maker . ?maker rdfs:label ?makerName . FILTER(LANG(?makerName) = "en") }
  OPTIONAL { ?item wdt:P4835 ?tvdb } OPTIONAL { ?item wdt:P4983 ?tmdbSeries } OPTIONAL { ?item wdt:P4947 ?tmdbFilm }
  OPTIONAL { ?item wdt:P345 ?imdb } OPTIONAL { ?item wdt:P8600 ?tvmaze } OPTIONAL { ?item wdt:P8729 ?anilist }
  OPTIONAL { ?item wdt:P648 ?openLibrary } OPTIONAL { ?item wdt:P8383 ?goodreads } OPTIONAL { ?item wdt:P675 ?googleBooks }
  OPTIONAL { ?item wdt:P212 ?isbn }
  OPTIONAL { ?item p:P179 ?seriesStatement . ?seriesStatement ps:P179 ?seriesItem . OPTIONAL { ?seriesStatement pq:P1545 ?ordinal }
    ?seriesItem rdfs:label ?seriesName . FILTER(LANG(?seriesName) = "en")
    BIND(CONCAT(STR(?seriesItem), "|", ?seriesName, "|", COALESCE(?ordinal, "")) AS ?seriesEntry) }
} GROUP BY ?id ?item`
		rows, err := f.sparql(ctx, query)
		if err != nil {
			return out, fmt.Errorf("wikidata: facts: %w", err)
		}
		claimed := map[string]int{}
		for _, row := range rows {
			claimed[row["id"].Value]++
		}
		for _, row := range rows {
			id, qid := row["id"].Value, qidOf(row["item"].Value)
			// An id two items claim says nothing.
			if claimed[id] > 1 || !qidPattern.MatchString(qid) {
				continue
			}
			facts := WorkFacts{QID: qid, OriginalTitle: row["original"].Value, Languages: split(row["languages"].Value),
				Countries: split(row["countries"].Value), Labels: split(row["labels"].Value), Makers: split(row["makers"].Value)}
			facts.IDs = factIDs(kind, qid, row)
			facts.Series = seriesPlace(row["series"].Value)
			if first := row["first"].Value; len(first) >= 10 {
				facts.Date = first[:10]
			}
			if minutes, err := strconv.ParseFloat(row["minutes"].Value, 64); err == nil && minutes >= 1 && minutes <= 1000 {
				facts.RuntimeMin = int(minutes)
			}
			for _, genre := range split(row["genres"].Value) {
				if name := genreName(genre); name != "" && !slices.Contains(facts.Genres, name) {
					facts.Genres = append(facts.Genres, name)
				}
			}
			out[id] = facts
		}
	}
	return out, f.addSummaries(ctx, out)
}

// addSummaries fills each work's English Wikipedia lead; a summary that cannot be read leaves that one empty.
func (f Fetcher) addSummaries(ctx context.Context, facts map[string]WorkFacts) error {
	qids := make([]string, 0, len(facts))
	for _, one := range facts {
		qids = append(qids, one.QID)
	}
	if len(qids) == 0 {
		return nil
	}
	biographies, err := f.Biographies(ctx, qids)
	if err != nil {
		return err
	}
	for id, one := range facts {
		one.Summary = biographies[one.QID].Extract
		facts[id] = one
	}
	return nil
}

// factIDs reads the ids a facts row carries; TMDB's is the film's or the series' as the kind says, the two being apart.
func factIDs(kind FactKind, qid string, row map[string]struct {
	Value string `json:"value"`
}) WorkIDs {
	ids := WorkIDs{QID: qid}
	ids.TVDB, _ = strconv.ParseInt(row["tvdbID"].Value, 10, 64)
	ids.TVmaze, _ = strconv.ParseInt(row["tvmazeID"].Value, 10, 64)
	tmdb := row["tmdbSeriesID"].Value
	if kind == FactFilm {
		tmdb = row["tmdbFilmID"].Value
	}
	ids.TMDB, _ = strconv.ParseInt(tmdb, 10, 64)
	if imdb := row["imdbID"].Value; imdbPattern.MatchString(imdb) {
		ids.IMDb = imdb
	}
	ids.OpenLibrary, ids.Goodreads = row["openLibraryID"].Value, row["goodreadsID"].Value
	ids.GoogleBooks, ids.ISBN = row["googleBooksID"].Value, strings.ReplaceAll(row["isbnID"].Value, "-", "")
	for _, anilist := range split(row["anilistIDs"].Value) {
		if id, err := strconv.ParseInt(anilist, 10, 64); err == nil {
			ids.AniList = append(ids.AniList, id)
		}
	}
	return ids
}

// seriesPlace reads a series entry, "item address|name|position".
func seriesPlace(entry string) *SeriesPlace {
	parts := strings.SplitN(entry, "|", 3)
	if len(parts) != 3 || !qidPattern.MatchString(qidOf(parts[0])) || parts[1] == "" {
		return nil
	}
	return &SeriesPlace{QID: qidOf(parts[0]), Name: parts[1], Position: strings.TrimSpace(parts[2])}
}

func split(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, "|")
}

// genreSuffixes say what kind of work a genre is about, which the work itself already says: "drama film" is "Drama".
var genreSuffixes = []string{" television series", " television program", " tv series", " film", " music"}

// genreName is a Wikidata genre as the other providers name genres: without its kind, each word capitalised.
func genreName(label string) string {
	name := strings.ToLower(strings.TrimSpace(label))
	for _, suffix := range genreSuffixes {
		if trimmed := strings.TrimSuffix(name, suffix); trimmed != name && trimmed != "" {
			name = trimmed
			break
		}
	}
	words := strings.Fields(name)
	for index, word := range words {
		runes := []rune(word)
		runes[0] = unicode.ToUpper(runes[0])
		words[index] = string(runes)
	}
	return strings.Join(words, " ")
}

// GenresOf answers the English names of each item's genres as Wikidata gives them, lower case; an item without any is
// absent.
func (f Fetcher) GenresOf(ctx context.Context, qids []string) (map[string][]string, error) {
	out := map[string][]string{}
	var items []string
	for _, qid := range qids {
		if qidPattern.MatchString(qid) && !slices.Contains(items, "wd:"+qid) {
			items = append(items, "wd:"+qid)
		}
	}
	for start := 0; start < len(items); start += factsPerQuery {
		batch := items[start:min(start+factsPerQuery, len(items))]
		rows, err := f.sparql(ctx, `SELECT ?item ?name WHERE { VALUES ?item { `+strings.Join(batch, " ")+` }
  ?item wdt:P136 ?genre . ?genre rdfs:label ?name . FILTER(LANG(?name) = "en") }`)
		if err != nil {
			return out, fmt.Errorf("wikidata: genres: %w", err)
		}
		for _, row := range rows {
			qid, name := qidOf(row["item"].Value), strings.ToLower(strings.TrimSpace(row["name"].Value))
			if name != "" && !slices.Contains(out[qid], name) {
				out[qid] = append(out[qid], name)
			}
		}
	}
	return out, nil
}
