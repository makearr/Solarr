package wikidata

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// mbidPattern is a MusicBrainz id; anything else never reaches a query.
var mbidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// collectionsPerQuery bounds one query's VALUES; the ids are indexed, so a larger batch costs the service little.
const collectionsPerQuery = 100

// AlbumFranchise is a franchise or series Wikidata puts an album in; Soundtrack when it is the franchise of the film or
// series the album is the soundtrack of (P406), not one named on the album itself.
type AlbumFranchise struct {
	Franchise
	Soundtrack bool
}

// AlbumFranchises answers, by MusicBrainz release group id (P436), the franchises and series each album is part of and
// those of the work it is the soundtrack of; an album Wikidata does not know is absent.
func (f Fetcher) AlbumFranchises(ctx context.Context, releaseGroups []string) (map[string][]AlbumFranchise, error) {
	out := map[string][]AlbumFranchise{}
	for _, batch := range quotedBatches(releaseGroups, mbidPattern) {
		rows, err := f.sparql(ctx, `SELECT ?id ?f ?fLabel ?parent ?film WHERE {
  VALUES ?id { `+batch+` } ?a wdt:P436 ?id .
  { ?a wdt:P179|wdt:P8345 ?f } UNION { ?film wdt:P406 ?a . ?film wdt:P179|wdt:P8345 ?f }
  OPTIONAL { ?f wdt:P361 ?parent }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". } }`)
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			id, qid := row["id"].Value, qidOf(row["f"].Value)
			if !qidPattern.MatchString(qid) {
				continue
			}
			soundtrack := row["film"].Value != ""
			franchises := out[id]
			index := slices.IndexFunc(franchises, func(franchise AlbumFranchise) bool { return franchise.QID == qid })
			if index < 0 {
				franchises = append(franchises, AlbumFranchise{Franchise: Franchise{QID: qid, Label: row["fLabel"].Value, Named: true}})
				index = len(franchises) - 1
			}
			franchises[index].Soundtrack = franchises[index].Soundtrack || soundtrack
			if parent := qidOf(row["parent"].Value); qidPattern.MatchString(parent) && !slices.Contains(franchises[index].Parents, parent) {
				franchises[index].Parents = append(franchises[index].Parents, parent)
			}
			out[id] = franchises
		}
	}
	return out, nil
}

// MusicSeriesItems answers the Wikidata item of each MusicBrainz series (P1407); one Wikidata does not know is absent.
func (f Fetcher) MusicSeriesItems(ctx context.Context, seriesMBIDs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, batch := range quotedBatches(seriesMBIDs, mbidPattern) {
		rows, err := f.sparql(ctx, `SELECT ?id ?s WHERE { VALUES ?id { `+batch+` } ?s wdt:P1407 ?id . }`)
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			if qid := qidOf(row["s"].Value); qidPattern.MatchString(qid) && (out[row["id"].Value] == "" || qid < out[row["id"].Value]) {
				out[row["id"].Value] = qid
			}
		}
	}
	return out, nil
}

// ItemFranchises answers, by item, the franchises and series it is part of (P179, P8345, P361) and the ones those are
// part of, sorted; an item with none is absent.
func (f Fetcher) ItemFranchises(ctx context.Context, qids []string) (map[string][]string, error) {
	out := map[string][]string{}
	var valid []string
	for _, qid := range qids {
		if qidPattern.MatchString(qid) && !slices.Contains(valid, qid) {
			valid = append(valid, qid)
		}
	}
	for start := 0; start < len(valid); start += collectionsPerQuery {
		batch := valid[start:min(start+collectionsPerQuery, len(valid))]
		values := make([]string, len(batch))
		for index, qid := range batch {
			values[index] = "wd:" + qid
		}
		rows, err := f.sparql(ctx, `SELECT ?s ?f ?parent WHERE {
  VALUES ?s { `+strings.Join(values, " ")+` } ?s wdt:P179|wdt:P8345|wdt:P361 ?f .
  OPTIONAL { ?f wdt:P361 ?parent } }`)
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			item := qidOf(row["s"].Value)
			for _, found := range []string{qidOf(row["f"].Value), qidOf(row["parent"].Value)} {
				if qidPattern.MatchString(found) && found != item && !slices.Contains(out[item], found) {
					out[item] = append(out[item], found)
				}
			}
		}
	}
	for item := range out {
		slices.Sort(out[item])
	}
	return out, nil
}

// quotedBatches are the valid ids, once each, as the quoted VALUES of one query per batch.
func quotedBatches(ids []string, valid *regexp.Regexp) []string {
	var kept []string
	seen := map[string]bool{}
	for _, id := range ids {
		if valid.MatchString(id) && !seen[id] {
			seen[id] = true
			kept = append(kept, strconv.Quote(id))
		}
	}
	var batches []string
	for start := 0; start < len(kept); start += collectionsPerQuery {
		batches = append(batches, strings.Join(kept[start:min(start+collectionsPerQuery, len(kept))], " "))
	}
	return batches
}
