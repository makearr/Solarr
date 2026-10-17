package wikidata

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// SeriesRef names a series by the ids Wikidata keeps for it; one of the two is enough.
type SeriesRef struct{ TVDB, TMDB int64 }

// SeriesTies is what Wikidata says ties one series to others: the franchises it is part of, and the series it is a
// spin-off, sequel or forerunner of.
type SeriesTies struct {
	Franchises []Franchise
	Related    []FranchiseSeries
}

// tiesPerQuery bounds the series one query asks about, so its address stays well inside what the service takes.
const tiesPerQuery = 50

// membersPerQuery bounds the franchises one query lists the series of.
const membersPerQuery = 20

// TiesOf answers what Wikidata knows of each series, a batch of them per query; series it does not know are absent.
func (f Fetcher) TiesOf(ctx context.Context, series []SeriesRef) (map[SeriesRef]SeriesTies, error) {
	out := map[SeriesRef]SeriesTies{}
	byTVDB, byTMDB := map[string]SeriesRef{}, map[string]SeriesRef{}
	for _, ref := range series {
		switch {
		case ref.TVDB > 0:
			byTVDB[strconv.FormatInt(ref.TVDB, 10)] = ref
		case ref.TMDB > 0:
			byTMDB[strconv.FormatInt(ref.TMDB, 10)] = ref
		}
	}
	for property, refs := range map[string]map[string]SeriesRef{"P4835": byTVDB, "P4983": byTMDB} {
		ids := make([]string, 0, len(refs))
		for id := range refs {
			ids = append(ids, id)
		}
		for start := 0; start < len(ids); start += tiesPerQuery {
			batch := ids[start:min(start+tiesPerQuery, len(ids))]
			if err := f.tiesBatch(ctx, property, batch, refs, out); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

func (f Fetcher) tiesBatch(ctx context.Context, property string, ids []string, refs map[string]SeriesRef, out map[SeriesRef]SeriesTies) error {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = strconv.Quote(id)
	}
	// An item with a TheTVDB or TMDB series id is a series: far cheaper to ask than its class.
	rows, err := f.sparql(ctx, `SELECT ?id ?f ?fLabel ?named ?parent ?tvdb ?tmdb ?start WHERE {
  VALUES ?id { `+strings.Join(values, " ")+` } ?s wdt:`+property+` ?id .
  { { ?s wdt:P179 ?f } UNION { ?s wdt:P8345 ?f } BIND(1 AS ?named) } UNION
  { { ?s wdt:P2512 ?f } UNION { ?f wdt:P2512 ?s } UNION { ?s wdt:P155 ?f } UNION { ?s wdt:P156 ?f } BIND(0 AS ?named) }
  OPTIONAL { ?f wdt:P4835 ?tvdb } OPTIONAL { ?f wdt:P4983 ?tmdb } OPTIONAL { ?f wdt:P580 ?start }
  OPTIONAL { ?f wdt:P361 ?parent }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". } }`)
	if err != nil {
		return err
	}
	for _, row := range rows {
		ref, known := refs[row["id"].Value]
		qid := qidOf(row["f"].Value)
		if !known || !qidPattern.MatchString(qid) {
			continue
		}
		ties := out[ref]
		if row["named"].Value == "1" {
			index := slices.IndexFunc(ties.Franchises, func(franchise Franchise) bool { return franchise.QID == qid })
			if index < 0 {
				ties.Franchises = append(ties.Franchises, Franchise{QID: qid, Label: row["fLabel"].Value, Named: true})
				index = len(ties.Franchises) - 1
			}
			if parent := qidOf(row["parent"].Value); qidPattern.MatchString(parent) && !slices.Contains(ties.Franchises[index].Parents, parent) {
				ties.Franchises[index].Parents = append(ties.Franchises[index].Parents, parent)
			}
		} else if row["tvdb"].Value != "" || row["tmdb"].Value != "" {
			ties.Related = mergeSeries(ties.Related, seriesOf(row, qid, row["fLabel"].Value))
		}
		out[ref] = ties
	}
	return nil
}

// FranchiseMembers lists the television series of each franchise: those part of it, and the item itself when it is one.
func (f Fetcher) FranchiseMembers(ctx context.Context, qids []string) (map[string][]FranchiseSeries, error) {
	out := map[string][]FranchiseSeries{}
	var valid []string
	for _, qid := range qids {
		if qidPattern.MatchString(qid) {
			valid = append(valid, "wd:"+qid)
		}
	}
	for start := 0; start < len(valid); start += membersPerQuery {
		batch := valid[start:min(start+membersPerQuery, len(valid))]
		query := `SELECT DISTINCT ?q ?m ?mLabel ?tvdb ?tmdb ?start WHERE {
  VALUES ?q { ` + strings.Join(batch, " ") + ` }
  { ?m wdt:P179 ?q } UNION { ?m wdt:P8345 ?q } UNION { ?q wdt:P31 [] . BIND(?q AS ?m) }
  OPTIONAL { ?m wdt:P4835 ?tvdb } OPTIONAL { ?m wdt:P4983 ?tmdb } FILTER(BOUND(?tvdb) || BOUND(?tmdb))
  OPTIONAL { ?m wdt:P580 ?start }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". } }`
		rows, err := f.sparql(ctx, query)
		if err != nil {
			return out, fmt.Errorf("wikidata: franchise members: %w", err)
		}
		for _, row := range rows {
			franchise, member := qidOf(row["q"].Value), qidOf(row["m"].Value)
			out[franchise] = mergeSeries(out[franchise], seriesOf(row, member, row["mLabel"].Value))
		}
	}
	return out, nil
}

func seriesOf(row map[string]struct {
	Value string `json:"value"`
}, qid, label string) FranchiseSeries {
	entry := FranchiseSeries{QID: qid, Label: label}
	entry.TVDB, _ = strconv.ParseInt(row["tvdb"].Value, 10, 64)
	entry.TMDB, _ = strconv.ParseInt(row["tmdb"].Value, 10, 64)
	if start := row["start"].Value; len(start) >= 4 {
		entry.Year, _ = strconv.Atoi(start[:4])
	}
	return entry
}

// mergeSeries adds a series to a list once, filling in what an earlier row of it lacked.
func mergeSeries(list []FranchiseSeries, entry FranchiseSeries) []FranchiseSeries {
	for index := range list {
		if list[index].QID != entry.QID {
			continue
		}
		if list[index].TVDB == 0 {
			list[index].TVDB = entry.TVDB
		}
		if list[index].TMDB == 0 {
			list[index].TMDB = entry.TMDB
		}
		if list[index].Year == 0 {
			list[index].Year = entry.Year
		}
		return list
	}
	return append(list, entry)
}
