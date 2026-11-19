package musicbrainz

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// lookup describes one cached provider call: the cache entry it lives under and what an unanswerable call means.
type lookup struct {
	kind, key string
	// what names the call in the error when every source failed.
	what string
	// noSource is the error when no configured source has the capability; nil reads as "found nothing".
	noSource error
	// notFoundWhenAll404 answers ErrNotFound, and remembers it, when every source answered 404.
	notFoundWhenAll404 bool
}

// through answers one call cache first, then from the first source with capability C, then from a stored copy when every source was down.
func through[C, T any](
	ctx context.Context, p Provider, priority Priority, call lookup, ask func(ctx context.Context, capable C) (T, error),
) (T, error) {
	var none T
	ctx = httpx.WithPriorityAtLeast(ctx, httpx.Priority(priority))
	r := routeFor[C](ctx, p)
	if value, ok := cachedJSON[T](ctx, p, r, call.kind, call.key); ok {
		return value, nil
	}
	if call.notFoundWhenAll404 && p.knownMissing(ctx, call.kind, call.key) {
		return none, ErrNotFound
	}
	answer, answered, errs := firstAnswer(ctx, p, r, ask)
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), call.kind, call.key, answer.Value)
		return answer.Value, nil
	}
	if len(errs) == 0 {
		return none, call.noSource
	}
	if call.notFoundWhenAll404 && !outage(errs) {
		p.rememberMissing(ctx, call.kind, call.key)
		return none, ErrNotFound
	}
	if stale, ok := staleJSON[T](ctx, p, r, call.kind, call.key, errs); ok {
		return stale, nil
	}
	return none, fmt.Errorf("musicbrainz: no source answered %s: %w", call.what, errors.Join(errs...))
}
