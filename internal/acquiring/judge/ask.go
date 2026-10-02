package judge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

// Searcher is what a search needs from a configured indexer connection.
type Searcher interface {
	Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error)
}

// Conn is one configured, enabled indexer ready to search.
type Conn struct {
	ID     int64
	Name   string
	Impl   string // "newznab" | "torznab"
	Client Searcher
	// Categories scope every search to the media type's categories; nil asks without one.
	Categories []int
	// Params are the search parameters the indexer's caps list for each function ("tvsearch" -> tvdbid, season, ...);
	// a function absent here is treated as unknown and tried.
	Params map[string][]string
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
}

// Answer is what one indexer gave for one tier's queries, up to its first failure.
type Answer struct {
	Raws    []indexer.RawItem
	Err     error
	TooSlow bool
}

// resultLimit bounds one indexer's response per query.
const resultLimit = 100

// AskEveryIndexer runs a tier's queries against every indexer at once and waits for all of them or the group
// deadline (ADR-37): one slow indexer does not delay the others' results.
func AskEveryIndexer(ctx context.Context, conns []Conn, queries []Query, deadline time.Duration) ([]Answer, error) {
	group, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	answers := make([]Answer, len(conns))
	var wait sync.WaitGroup
	for index, conn := range conns {
		wait.Go(func() { answers[index] = askIndexer(group, conn, queries) })
	}
	wait.Wait()
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
		raws, err := conn.Client.Search(ctx, indexer.SearchRequest{
			Function: query.Function, Query: query.Text, Extra: query.Extra, Categories: conn.Categories, Limit: resultLimit,
		})
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
