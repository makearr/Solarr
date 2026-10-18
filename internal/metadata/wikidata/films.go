package wikidata

import (
	"context"
	"slices"
	"strconv"
	"strings"
)

// FilmFranchises answers the franchises Wikidata puts each film in ("part of the series", "media franchise"), by the
// film's TMDB id, a batch of films per query; a film it does not know is absent. A franchise carries the franchises it
// is part of, so a phase can be joined to its cinematic universe.
func (f Fetcher) FilmFranchises(ctx context.Context, tmdbIDs []int64) (map[int64][]Franchise, error) {
	out := map[int64][]Franchise{}
	for start := 0; start < len(tmdbIDs); start += tiesPerQuery {
		batch := tmdbIDs[start:min(start+tiesPerQuery, len(tmdbIDs))]
		values := make([]string, len(batch))
		for index, id := range batch {
			values[index] = strconv.Quote(strconv.FormatInt(id, 10))
		}
		rows, err := f.sparql(ctx, `SELECT ?id ?f ?fLabel ?parent WHERE {
  VALUES ?id { `+strings.Join(values, " ")+` } ?s wdt:P4947 ?id .
  { ?s wdt:P179 ?f } UNION { ?s wdt:P8345 ?f }
  OPTIONAL { ?f wdt:P361 ?parent }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". } }`)
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			id, err := strconv.ParseInt(row["id"].Value, 10, 64)
			qid := qidOf(row["f"].Value)
			if err != nil || !qidPattern.MatchString(qid) {
				continue
			}
			franchises := out[id]
			index := slices.IndexFunc(franchises, func(franchise Franchise) bool { return franchise.QID == qid })
			if index < 0 {
				franchises = append(franchises, Franchise{QID: qid, Label: row["fLabel"].Value, Named: true})
				index = len(franchises) - 1
			}
			if parent := qidOf(row["parent"].Value); qidPattern.MatchString(parent) && !slices.Contains(franchises[index].Parents, parent) {
				franchises[index].Parents = append(franchises[index].Parents, parent)
			}
			out[id] = franchises
		}
	}
	return out, nil
}
