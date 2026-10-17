package wikidata

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
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
	// Parents are the franchises this one is part of: a phase of a cinematic universe.
	Parents []string
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
