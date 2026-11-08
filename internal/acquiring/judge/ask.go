package judge

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

// Searcher is what a search needs from a configured indexer connection.
type Searcher interface {
	Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error)
}

// Conn is one configured, enabled indexer ready to search.
type Conn struct {
	ID   int64
	Name string
	Impl string // indexer.ImplNewznab, indexer.ImplTorznab, ...
	// Priority is the indexer's, 1 to 50, lower preferred: it breaks ties between otherwise equal releases.
	Priority int64
	Client   Searcher
	// Categories scope every search to the media type's categories; nil asks without one.
	Categories []int
	// Params are the search parameters the indexer's caps list for each function ("tvsearch" -> tvdbid, season, ...);
	// a function absent here is treated as unknown and tried.
	Params map[string][]string
	// Seal removes the indexer's credential from a download link before it is stored; nil leaves the link as it is.
	Seal func(link string) string
}

// Takes reports whether the indexer accepts every parameter a structured query uses; unknown caps accept anything.
func (c Conn) Takes(function string, params []string) bool {
	known, ok := c.Params[function]
	if !ok || known == nil {
		return true
	}
	for _, want := range params {
		found := false
		for _, have := range known {
			if have == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Query is one indexer request of a tier. Extra carries structured parameters; Needs lists the ones an indexer must
// accept for the query to be worth sending.
type Query struct {
	Function string
	Text     string
	Extra    map[string]string
	Needs    []string
	// Limit bounds the indexer's answer to this query; zero takes resultLimit.
	Limit int
	// MoreCategories widen an indexer's own categories for this query (anime's 5070 besides TV's 5000); an indexer
	// asked without categories is asked without them still.
	MoreCategories []int
}

// Answer is what one indexer gave for one tier's queries, up to its first failure.
type Answer struct {
	Raws    []indexer.RawItem
	Err     error
	TooSlow bool
}

// maxIndexersAtOnce bounds how many indexers one search asks at the same moment.
const maxIndexersAtOnce = 8

// searches shares one request among identical ones made at the same time (an album and its twin, a retry): bunches of
// searches cost the indexer one request.
var searches parallel.Flight[[]indexer.RawItem]

// resultLimit bounds one indexer's response per query.
const resultLimit = 100

// AskEveryIndexer runs a tier's queries against every indexer at once and waits for all of them or the group
// deadline (ADR-37): one slow indexer does not delay the others' results.
func AskEveryIndexer(ctx context.Context, conns []Conn, queries []Query, deadline time.Duration) ([]Answer, error) {
	group, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	answers := make([]Answer, len(conns))
	// Highest priority (lowest number) starts first, so when more indexers than slots are asked the preferred ones are not the ones waiting.
	order := make([]int, len(conns))
	for index := range order {
		order[index] = index
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(conns[a].Priority, conns[b].Priority) })
	asked := parallel.Do(group, len(conns), maxIndexersAtOnce, func(ctx context.Context, position int) error {
		index := order[position]
		answers[index] = askIndexer(ctx, conns[index], queries)
		return nil
	})
	for position, err := range asked {
		if err != nil {
			answers[order[position]].Err = err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for index := range answers {
		if answers[index].Err != nil && errors.Is(group.Err(), context.DeadlineExceeded) && errors.Is(answers[index].Err, context.DeadlineExceeded) {
			answers[index].TooSlow = true
			answers[index].Err = fmt.Errorf("no answer within %s; its releases are missing from this search", deadline)
		}
	}
	return answers, nil
}

func askIndexer(ctx context.Context, conn Conn, queries []Query) Answer {
	var answer Answer
	seen := map[string]bool{}
	for _, query := range queries {
		if len(query.Needs) > 0 && !conn.Takes(query.Function, query.Needs) {
			continue
		}
		categories := conn.Categories
		if len(categories) > 0 && len(query.MoreCategories) > 0 {
			categories = append(slices.Clone(categories), query.MoreCategories...)
		}
		request := indexer.SearchRequest{
			Function: query.Function, Query: query.Text, Extra: query.Extra, Categories: categories, Limit: cmp.Or(query.Limit, resultLimit),
		}
		raws, _, err := searches.Do(searchKey(conn.ID, request), func() ([]indexer.RawItem, error) { return conn.Client.Search(ctx, request) })
		if err != nil {
			answer.Err = err
			return answer
		}
		for _, raw := range raws {
			if !seen[raw.GUID] {
				seen[raw.GUID] = true
				answer.Raws = append(answer.Raws, raw)
			}
		}
	}
	return answer
}

// searchKey names a request to one indexer; two with the same key ask the same question.
func searchKey(indexerID int64, request indexer.SearchRequest) string {
	extra := make([]string, 0, len(request.Extra))
	for key, value := range request.Extra {
		extra = append(extra, key+"="+value)
	}
	slices.Sort(extra)
	return fmt.Sprint(indexerID, "|", request.Function, "|", request.Query, "|", request.Categories, "|", request.Limit, "|", extra)
}
