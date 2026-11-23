package comic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/comic"
	"github.com/makearr/Solarr/internal/metadata/comicvine"
)

type creatorsOff struct{}

func (creatorsOff) Configured() bool { return false }
func (creatorsOff) CreatorSeries(context.Context, string) ([]comicvine.Series, error) {
	return nil, errors.New("must not be asked")
}

func TestFollowLimitIsLargerForABackCatalogue(t *testing.T) {
	following := comic.Following{}
	if got := following.FollowLimit(media.Follow{}); got != 5 {
		t.Errorf("limit = %d, want 5", got)
	}
	if got := following.FollowLimit(media.Follow{BackCatalogue: true}); got != 25 {
		t.Errorf("back-catalogue limit = %d, want 25", got)
	}
}

func TestFollowWithoutAComicVineKeyIsRefusedBeforeAskingAnyone(t *testing.T) {
	following := comic.Following{Source: creatorsOff{}}
	_, err := following.FollowCandidates(t.Context(), media.Follow{SubjectType: "person", SubjectID: 1})
	if !errors.Is(err, media.ErrNoFollowSource) {
		t.Errorf("err = %v, want ErrNoFollowSource", err)
	}
	if _, err := (comic.Module{}).FollowCandidates(t.Context(), media.Follow{}); !errors.Is(err, media.ErrNoFollowSource) {
		t.Errorf("module without a source: err = %v", err)
	}
}
