package music

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestArtworkMissIsOnlyAFailureThatWillRepeat(t *testing.T) {
	for name, test := range map[string]struct {
		err  error
		want bool
	}{
		"no error":         {nil, false},
		"dead image link":  {fmt.Errorf("fetch image: %w", &httpx.StatusError{Status: http.StatusNotFound}), true},
		"forbidden":        {&httpx.StatusError{Status: http.StatusForbidden}, true},
		"server down":      {fmt.Errorf("fetch image: %w", &httpx.StatusError{Status: http.StatusServiceUnavailable}), false},
		"rate limited":     {&httpx.StatusError{Status: http.StatusTooManyRequests}, false},
		"oversize file":    {fmt.Errorf("read image: %w", &httpx.TooLargeError{Limit: 1}), true},
		"undecodable":      {fmt.Errorf("process image: %w: boom", errNotAnImage), true},
		"root removed":     {fmt.Errorf("root folder 3: %w", errRootNotConfigured), true},
		"transport broken": {errors.New("connection reset"), false},
	} {
		if got := artworkMiss(test.err); got != test.want {
			t.Errorf("%s: artworkMiss = %v, want %v", name, got, test.want)
		}
	}
}
