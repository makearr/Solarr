package adapters_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type recordedAffinity struct{ keys map[string]int64 }

func (r *recordedAffinity) SetPlacementAffinity(_ context.Context, _ int64, key string, root, _ int64) error {
	r.keys[key] = root
	return nil
}

type fixedAdding struct{}

func (fixedAdding) LookupWorks(context.Context, media.AddLookup) ([]media.AddCandidate, error) {
	return nil, nil
}
func (fixedAdding) AddWork(context.Context, media.AddRequest) (media.AddResult, error) {
	return media.AddResult{WorkID: 7}, nil
}

func TestWorkAdding_ChosenRootBecomesPlacementAffinity(t *testing.T) {
	root := int64(3)
	for name, test := range map[string]struct {
		mediaType string
		chosen    *int64
		want      map[string]int64
	}{
		"a type with a key records the root": {mediaType: "movie", chosen: &root, want: map[string]int64{"movie:dune:2021": 3}},
		"no root chosen records nothing":     {mediaType: "movie", want: map[string]int64{}},
		"a type without a key ignores it":    {mediaType: "music", chosen: &root, want: map[string]int64{}},
	} {
		t.Run(name, func(t *testing.T) {
			cache := newSweepCache(t)
			category, err := cache.CreateCategory(t.Context(), test.mediaType, "Library", 1)
			if err != nil {
				t.Fatalf("CreateCategory: %v", err)
			}
			affinity := &recordedAffinity{keys: map[string]int64{}}
			adding := adapters.WorkAdding{
				Categories: adapters.Categories{Cache: cache},
				Adding:     func(string) (media.Adding, bool) { return fixedAdding{}, true },
				AddKinds:   func(string) []media.Choice { return []media.Choice{{Key: "work"}} },
				Placement: &adapters.AddPlacement{
					Titled:   func(context.Context, string, int64, int64) (string, int64, error) { return "Dune", 2021, nil },
					Affinity: affinity, Clock: clock.NewFakeAtEpoch(),
					Keys: map[string]func(string, int64) string{"movie": func(title string, year int64) string {
						return fmt.Sprintf("movie:%s:%d", strings.ToLower(title), year)
					}},
				},
			}
			if _, err := adding.Add(t.Context(), apiserver.AddWorkRequest{CategoryID: category.ID, RootFolderID: test.chosen}); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if len(affinity.keys) != len(test.want) || (len(test.want) > 0 && affinity.keys["movie:dune:2021"] != 3) {
				t.Fatalf("affinity = %v, want %v", affinity.keys, test.want)
			}
		})
	}
}
