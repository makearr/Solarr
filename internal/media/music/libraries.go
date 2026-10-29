package music

import (
	"context"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// Libraries answers the music library an album or artist is looked up for, so its own provider order rules the lookup;
// 0 is none, which takes the default order.
type Libraries interface {
	OfWork(ctx context.Context, workID int64) int64
	OfPerson(ctx context.Context, personID int64) int64
}

// workLibrary is the library the work is looked up for, 0 without a resolver.
func (j *EnrichJob) workLibrary(ctx context.Context, workID int64) int64 {
	if j.libraries == nil {
		return 0
	}
	return j.libraries.OfWork(ctx, workID)
}

// forPerson puts on ctx the library the person is looked up for.
func (j *EnrichJob) forPerson(ctx context.Context, personID int64) context.Context {
	if j.libraries == nil {
		return ctx
	}
	return musicbrainz.ForLibrary(ctx, j.libraries.OfPerson(ctx, personID))
}
