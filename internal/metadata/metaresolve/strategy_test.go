package metaresolve_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/metaresolve"
)

func TestFallThrough(t *testing.T) {
	down := errors.New("down")
	var asked []string
	ask := func(answers map[string]error) func(context.Context, string) (string, bool, error) {
		return func(_ context.Context, source string) (string, bool, error) {
			asked = append(asked, source)
			err, can := answers[source]
			return "from " + source, can, err
		}
	}

	answer, answered, errs := metaresolve.FallThrough(t.Context(), []string{"proxy", "mirror", "direct"},
		ask(map[string]error{"proxy": down, "direct": nil}))
	if !answered || answer.Source != "direct" || answer.Value != "from direct" || errs != nil {
		t.Errorf("answer %+v, answered %v, errs %v; want direct's, past the failed proxy and the mirror that cannot ask", answer, answered, errs)
	}
	if want := []string{"proxy", "mirror", "direct"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("asked %v, want %v", asked, want)
	}

	asked = nil
	if _, answered, errs := metaresolve.FallThrough(t.Context(), []string{"proxy", "direct"}, ask(map[string]error{"proxy": down, "direct": down})); answered || len(errs) != 2 {
		t.Errorf("all failing: answered %v, errs %v; want both failures", answered, errs)
	}
	if _, answered, errs := metaresolve.FallThrough(t.Context(), []string{"mirror"}, ask(map[string]error{})); answered || errs != nil {
		t.Errorf("none can ask: answered %v, errs %v; want no answer and no failure", answered, errs)
	}
}

// Every source is asked at once: three that each take a while finish in about one's time, not the sum.
func TestAskAll(t *testing.T) {
	var mu sync.Mutex
	running, most := 0, 0
	failed := errors.New("failed")
	answers := metaresolve.AskAll(t.Context(), []string{"tmdb", "anilist", "tvmaze"}, func(_ context.Context, source string) (int, error) {
		mu.Lock()
		running++
		most = max(most, running)
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		if source == "anilist" {
			return 0, failed
		}
		return len(source), nil
	})
	got := []string{}
	for _, answer := range answers {
		got = append(got, answer.Source)
	}
	if !reflect.DeepEqual(got, []string{"tmdb", "anilist", "tvmaze"}) || answers[0].Value != 4 || !errors.Is(answers[1].Err, failed) || answers[2].Value != 6 {
		t.Errorf("answers = %+v", answers)
	}
	if most != 3 {
		t.Errorf("at most %d sources were asked at once, want all 3", most)
	}
}
