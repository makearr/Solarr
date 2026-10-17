package wikidata

import (
	"context"
	"regexp"
	"strconv"
)

// imdbPattern is an IMDb title id, the only shape that reaches a query.
var imdbPattern = regexp.MustCompile(`^tt[0-9]{7,10}$`)

// WorkIDs are the ids other providers know a series or a film by, as Wikidata's item records them.
type WorkIDs struct {
	QID                string
	TVDB, TMDB, TVmaze int64
	IMDb               string
	AniList            []int64
}

// SeriesIDs answers the ids Wikidata records for the series TheTVDB's id names, else TMDB's, else IMDb's; false when no
// one item answers, for an id two items claim says nothing.
func (f Fetcher) SeriesIDs(ctx context.Context, known WorkIDs) (WorkIDs, bool, error) {
	var match string
	switch {
	case known.TVDB > 0:
		match = `?item wdt:P4835 ` + strconv.Quote(strconv.FormatInt(known.TVDB, 10)) + ` .`
	case known.TMDB > 0:
		match = `?item wdt:P4983 ` + strconv.Quote(strconv.FormatInt(known.TMDB, 10)) + ` .`
	case imdbPattern.MatchString(known.IMDb):
		match = `?item wdt:P345 ` + strconv.Quote(known.IMDb) + ` .`
	default:
		return WorkIDs{}, false, nil
	}
	return f.itemIDs(ctx, match, `OPTIONAL { ?item wdt:P4835 ?tvdb } OPTIONAL { ?item wdt:P4983 ?tmdb } OPTIONAL { ?item wdt:P345 ?imdb }
  OPTIONAL { ?item wdt:P8600 ?tvmaze } OPTIONAL { ?item wdt:P8729 ?anilist }`)
}

// MovieIDs answers the ids Wikidata records for the film TMDB's id names, else IMDb's.
func (f Fetcher) MovieIDs(ctx context.Context, tmdb int64, imdb string) (WorkIDs, bool, error) {
	var match string
	switch {
	case tmdb > 0:
		match = `?item wdt:P4947 ` + strconv.Quote(strconv.FormatInt(tmdb, 10)) + ` .`
	case imdbPattern.MatchString(imdb):
		match = `?item wdt:P345 ` + strconv.Quote(imdb) + ` .`
	default:
		return WorkIDs{}, false, nil
	}
	return f.itemIDs(ctx, match, `OPTIONAL { ?item wdt:P4947 ?tmdb } OPTIONAL { ?item wdt:P345 ?imdb }`)
}

func (f Fetcher) itemIDs(ctx context.Context, match, optional string) (WorkIDs, bool, error) {
	rows, err := f.sparql(ctx, `SELECT ?item ?tvdb ?tmdb ?imdb ?tvmaze ?anilist WHERE { `+match+` `+optional+` } LIMIT 50`)
	if err != nil || len(rows) == 0 {
		return WorkIDs{}, false, err
	}
	var ids WorkIDs
	for _, row := range rows {
		qid := qidOf(row["item"].Value)
		switch {
		case ids.QID == "":
			ids.QID = qid
		case ids.QID != qid:
			return WorkIDs{}, false, nil
		}
		// An item with two values of one id is ambiguous about it: the first is kept, as Wikidata ranks it.
		if value, err := strconv.ParseInt(row["tvdb"].Value, 10, 64); err == nil && ids.TVDB == 0 {
			ids.TVDB = value
		}
		if value, err := strconv.ParseInt(row["tmdb"].Value, 10, 64); err == nil && ids.TMDB == 0 {
			ids.TMDB = value
		}
		if value, err := strconv.ParseInt(row["tvmaze"].Value, 10, 64); err == nil && ids.TVmaze == 0 {
			ids.TVmaze = value
		}
		if value := row["imdb"].Value; imdbPattern.MatchString(value) && ids.IMDb == "" {
			ids.IMDb = value
		}
		if value, err := strconv.ParseInt(row["anilist"].Value, 10, 64); err == nil && !containsID(ids.AniList, value) {
			ids.AniList = append(ids.AniList, value)
		}
	}
	return ids, ids.QID != "", nil
}

func containsID(ids []int64, id int64) bool {
	for _, one := range ids {
		if one == id {
			return true
		}
	}
	return false
}
