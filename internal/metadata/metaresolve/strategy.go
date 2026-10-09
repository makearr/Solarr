package metaresolve

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/parallel"
)

// Answer is what one source said.
type Answer[S, T any] struct {
	Source S
	Value  T
	Err    error
}

// FallThrough asks the sources in order and stops at the first that answers (ADR-29: the proxies first, the
// direct source last). ask says can false for a source that cannot answer this question at all; it is passed
// over without counting as a failure. answered is false when none answered, and errs then holds every failure in
// order: none means no source could even be asked.
func FallThrough[S, T any](
	ctx context.Context, sources []S, ask func(ctx context.Context, source S) (value T, can bool, err error),
) (answer Answer[S, T], answered bool, errs []error) {
	for _, source := range sources {
		value, can, err := ask(ctx, source)
		if !can {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		return Answer[S, T]{Source: source, Value: value}, true, nil
	}
	return Answer[S, T]{}, false, errs
}

// AskAll asks every source at once, each paced by its own rate bucket, and answers each one's reply at its index;
// the caller merges them in its own priority.
func AskAll[S, T any](ctx context.Context, sources []S, ask func(ctx context.Context, source S) (T, error)) []Answer[S, T] {
	answers := make([]Answer[S, T], len(sources))
	errs := parallel.Do(ctx, len(sources), 0, func(ctx context.Context, index int) error {
		value, err := ask(ctx, sources[index])
		answers[index] = Answer[S, T]{Source: sources[index], Value: value}
		return err
	})
	for index, err := range errs {
		answers[index].Source, answers[index].Err = sources[index], err
	}
	return answers
}
