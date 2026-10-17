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
)

// WorkFacts are what Wikidata's item, and its English Wikipedia article, say of a film, a series or an album.
type WorkFacts struct {
	QID           string
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
  (GROUP_CONCAT(DISTINCT ?makerName; separator="|") AS ?makers) WHERE {
  VALUES ?id { ` + strings.Join(values, " ") + ` } ?item wdt:` + string(kind) + ` ?id .
  OPTIONAL { ?item wdt:P1476 ?title }
  OPTIONAL { ?item wdt:P577|wdt:P580 ?date }
  OPTIONAL { ?item wdt:P2047 ?runtime }
  OPTIONAL { ?item wdt:P136 ?genre . ?genre rdfs:label ?genreLabel . FILTER(LANG(?genreLabel) = "en") }
  OPTIONAL { ?item wdt:P364|wdt:P407 ?languageItem . ?languageItem wdt:P218 ?language }
  OPTIONAL { ?item wdt:P495 ?countryItem . ?countryItem wdt:P297 ?country }
  OPTIONAL { ?item wdt:P264 ?label . ?label rdfs:label ?labelName . FILTER(LANG(?labelName) = "en") }
  OPTIONAL { ?item wdt:P57|wdt:P170|wdt:P175 ?maker . ?maker rdfs:label ?makerName . FILTER(LANG(?makerName) = "en") }
} GROUP BY ?id ?item`
		rows, err := f.sparql(ctx, query)
		if err != nil {
			if rows, err = f.sparql(ctx, query); err != nil {
				return out, fmt.Errorf("wikidata: facts: %w", err)
			}
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
