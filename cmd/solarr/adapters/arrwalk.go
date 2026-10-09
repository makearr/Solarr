package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// arrSource is one *Arr app's half of an import: what it lists, and how one listed item becomes Solarr's.
type arrSource[T any] struct {
	connect  func(ctx context.Context) error
	list     func(ctx context.Context) ([]T, error)
	describe func(item T) arrItem
	// sync makes one item Solarr's in the library its root folder belongs to; an error still keeps its counts.
	sync func(ctx context.Context, categoryID int64, item T) (library.ArrImportCounts, error)
	// workers is how many items are synced at once; each waits mostly on providers that pace themselves.
	workers int
}

// arrItem is what the walk needs to know of a listed item.
type arrItem struct {
	Name, RootFolder string
	Monitored        bool
}

// arrWalk is the walk every *Arr import shares, its own half of an import run.
type arrWalk struct {
	importID int64
	options  apiserver.ArrSyncOptions
	// place answers the library an *Arr root folder, already rewritten, belongs to.
	place    func(rootFolder string) (int64, bool)
	progress *ArrProgress
}

// walkArr syncs every item the options want, best-effort per item: one that fails or belongs to no library does not
// stop the rest. Failing to connect or to list is the one thing that fails the whole run.
func walkArr[T any](ctx context.Context, walk arrWalk, source arrSource[T]) (library.ArrImportCounts, error) {
	if err := source.connect(ctx); err != nil {
		return library.ArrImportCounts{}, fmt.Errorf("connect: %w", err)
	}
	all, err := source.list(ctx)
	if err != nil {
		return library.ArrImportCounts{}, fmt.Errorf("list: %w", err)
	}
	var chosen []T
	for _, item := range all {
		monitored := source.describe(item).Monitored
		if (monitored && walk.options.Monitored) || (!monitored && walk.options.Unmonitored) {
			chosen = append(chosen, item)
		}
	}
	total := int64(len(chosen))
	walk.progress.set(walk.importID, 0, total)

	var (
		mu       sync.Mutex
		walked   int64
		counts   library.ArrImportCounts
		failures []string
		group    sync.WaitGroup
		gate     = make(chan struct{}, max(1, source.workers))
	)
	for _, item := range chosen {
		described := source.describe(item)
		group.Add(1)
		gate <- struct{}{}
		go func() {
			defer group.Done()
			defer func() { <-gate }()
			var got library.ArrImportCounts
			categoryID, placed := walk.place(rewritePath(described.RootFolder, walk.options.PathRewrites))
			err := fmt.Errorf("no category matches root folder %q", described.RootFolder)
			if placed {
				got, err = source.sync(ctx, categoryID, item)
			}
			mu.Lock()
			defer mu.Unlock()
			counts = addCounts(counts, got)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", described.Name, err))
			}
			walked++
			walk.progress.set(walk.importID, walked, total)
		}()
	}
	group.Wait()
	if len(failures) > 0 {
		return counts, errors.New(strings.Join(failures, "; "))
	}
	return counts, nil
}

func addCounts(a, b library.ArrImportCounts) library.ArrImportCounts {
	return library.ArrImportCounts{
		ArtistsCreated: a.ArtistsCreated + b.ArtistsCreated, ArtistsMatched: a.ArtistsMatched + b.ArtistsMatched,
		AlbumsCreated: a.AlbumsCreated + b.AlbumsCreated, AlbumsMatched: a.AlbumsMatched + b.AlbumsMatched,
		ItemsParked: a.ItemsParked + b.ItemsParked,
	}
}
