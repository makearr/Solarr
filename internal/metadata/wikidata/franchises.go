package wikidata

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// sparqlEndpoint is Wikidata's query service, a separate host from the entity API.
const sparqlEndpoint = "https://query.wikidata.org/sparql"

var qidPattern = regexp.MustCompile(`^Q[0-9]{1,12}$`)

// FranchiseSeries is one television series of a franchise, by the ids other providers know it by.
type FranchiseSeries struct {
	QID, Label string
	TVDB, TMDB int64
	Year       int
}

// Franchise is the media franchise Wikidata puts a series in. Named when the item is a franchise ("part of the series",
// "media franchise"), not a series the series is tied to by a spin-off or a sequel claim.
type Franchise struct {
	QID, Label string
	Named      bool
}

type sparqlResponse struct {
	Results struct {
		Bindings []map[string]struct {
			Value string `json:"value"`
		} `json:"bindings"`
	} `json:"results"`
}

func (f Fetcher) sparql(ctx context.Context, query string) ([]map[string]struct {
	Value string `json:"value"`
}, error) {
	var answer sparqlResponse
	address := sparqlEndpoint + "?" + url.Values{"query": {query}, "format": {"json"}}.Encode()
	if err := getJSON(ctx, f.wikidataClient, address, &answer); err != nil {
		return nil, fmt.Errorf("wikidata: sparql: %w", err)
	}
	return answer.Results.Bindings, nil
}

func qidOf(entityURL string) string { return entityURL[strings.LastIndex(entityURL, "/")+1:] }

// SeriesFranchise answers the franchise of the series (a "part of the series", "media franchise", spin-off or
// follows/followed-by claim: the item the series is tied to) that has
// this TheTVDB or TMDB id, and whether it has one.
func (f Fetcher) SeriesFranchise(ctx context.Context, tvdb, tmdb int64) (Franchise, bool, error) {
	var match string
	switch {
	case tvdb > 0:
		match = fmt.Sprintf(`?s wdt:P4835 "%d" .`, tvdb)
	case tmdb > 0:
		match = fmt.Sprintf(`?s wdt:P4983 "%d" .`, tmdb)
	default:
		return Franchise{}, false, nil
	}
	rows, err := f.sparql(ctx, `SELECT ?f ?fLabel ?named WHERE { `+match+`
  { { ?s wdt:P179 ?f } UNION { ?s wdt:P8345 ?f } BIND(1 AS ?named) } UNION
  { { ?s wdt:P2512 ?f } UNION { ?f wdt:P2512 ?s } UNION { ?s wdt:P155 ?f } UNION { ?s wdt:P156 ?f } BIND(0 AS ?named) }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". } } ORDER BY DESC(?named) LIMIT 5`)
	if err != nil || len(rows) == 0 {
		return Franchise{}, false, err
	}
	qid := qidOf(rows[0]["f"].Value)
	if !qidPattern.MatchString(qid) {
		return Franchise{}, false, nil
	}
	return Franchise{QID: qid, Label: rows[0]["fLabel"].Value, Named: rows[0]["named"].Value == "1"}, true, nil
}

// FranchiseSeriesOf lists the television series of a franchise: those that are part of it, and the franchise item
// itself when it is a series.
func (f Fetcher) FranchiseSeriesOf(ctx context.Context, qid string) ([]FranchiseSeries, error) {
	if !qidPattern.MatchString(qid) {
		return nil, fmt.Errorf("wikidata: %q is not an item id", qid)
	}
	rows, err := f.sparql(ctx, `SELECT DISTINCT ?m ?mLabel ?tvdb ?tmdb ?start WHERE {
  { ?m wdt:P179 wd:`+qid+` } UNION { ?m wdt:P8345 wd:`+qid+` } UNION { ?m wdt:P2512 wd:`+qid+` } UNION { wd:`+qid+` wdt:P2512 ?m } UNION
  { ?m wdt:P155 wd:`+qid+` } UNION { ?m wdt:P156 wd:`+qid+` } UNION { BIND(wd:`+qid+` AS ?m) }
  ?m wdt:P31/wdt:P279* wd:Q5398426 .
  OPTIONAL { ?m wdt:P4835 ?tvdb } OPTIONAL { ?m wdt:P4983 ?tmdb } OPTIONAL { ?m wdt:P580 ?start }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". } }`)
	if err != nil {
		return nil, err
	}
	byQID := map[string]*FranchiseSeries{}
	var order []string
	for _, row := range rows {
		member := qidOf(row["m"].Value)
		entry, seen := byQID[member]
		if !seen {
			entry = &FranchiseSeries{QID: member, Label: row["mLabel"].Value}
			byQID[member] = entry
			order = append(order, member)
		}
		if value := row["tvdb"].Value; value != "" && entry.TVDB == 0 {
			entry.TVDB, _ = strconv.ParseInt(value, 10, 64)
		}
		if value := row["tmdb"].Value; value != "" && entry.TMDB == 0 {
			entry.TMDB, _ = strconv.ParseInt(value, 10, 64)
		}
		if start := row["start"].Value; len(start) >= 4 && entry.Year == 0 {
			entry.Year, _ = strconv.Atoi(start[:4])
		}
	}
	out := make([]FranchiseSeries, 0, len(order))
	for _, member := range order {
		out = append(out, *byQID[member])
	}
	return out, nil
}
