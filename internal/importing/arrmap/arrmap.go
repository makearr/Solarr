// Package arrmap is what every *Arr import shares: the walk over one app's items and the monitoring of what is held.
// Each media type owns its app's mapping behind Importer (documentation/arr-import.md).
package arrmap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

// Options are the per-run choices a mapping reads.
type Options struct {
	Monitored, Unmonitored bool
	// Present also chooses the items the *Arr holds files for, whatever their monitored flag.
	Present bool
	// Albums is "all", "missing" (the *Arr lacks a track) or "complete".
	Albums string
	// ProfileMap gives each *Arr quality profile's items a Solarr profile; nil keeps the library's, an unnamed one
	// takes the Solarr profile of the same name.
	ProfileMap map[int64]*int64
	// MonitoringOnly sets the monitored flag of what is held and adds nothing.
	MonitoringOnly bool
	// Limit stops after the first this many chosen items; zero is all of them.
	Limit int
}

// Run is one import as a mapping sees it; the run's recording stays with the caller.
type Run struct {
	Client  arrimport.LidarrClient
	Options Options
	// Place answers the library an *Arr root folder belongs to.
	Place func(rootFolder string) (int64, bool)
	// Progress tells how far the walk is.
	Progress func(done, total int64)
	// Record adds one item to the run's list, which a revert reads.
	Record func(ctx context.Context, item library.NewArrImportItem) error
	// Link remembers what an *Arr item is here, for sending monitoring back.
	Link func(ctx context.Context, sourceKind string, sourceID int64, localKind string, localID int64)
}

// Importer is one app's mapping onto a media type.
type Importer interface {
	Import(ctx context.Context, run Run) (library.ArrImportCounts, error)
	// Preview reads the app's items, root folders, profiles and Media Management, changing nothing.
	Preview(ctx context.Context, client arrimport.LidarrClient) (PreviewSource, error)
}

// PreviewItem is one *Arr item as a preview counts it.
type PreviewItem struct {
	RootFolder string
	Monitored  bool
	ProfileID  int64
	// Provider and ExternalID find it here; an empty ExternalID is an item the import skips.
	Provider, ExternalID string
	// Person says the id is a person's (a Lidarr artist), not a work's.
	Person bool
}

// PreviewSource is what one app gives a preview.
type PreviewSource struct {
	ItemKind  string
	MediaType string
	Items     []PreviewItem
	Roots     []string
	Profiles  map[int64]string
	Settings  arrimport.MediaManagement
}

// PreviewClient is what a preview reads of every app beside its items.
type PreviewClient interface {
	RootFolders(ctx context.Context) ([]arrimport.LidarrRootFolder, error)
	MediaManagement(ctx context.Context) (arrimport.MediaManagement, error)
}

// ReadPreview completes a preview with the app's root folders, profiles and Media Management; only the last can fail it.
func ReadPreview(ctx context.Context, source PreviewSource, client PreviewClient, profiles map[int64]string) (PreviewSource, error) {
	folders, _ := client.RootFolders(ctx)
	for _, folder := range folders {
		source.Roots = append(source.Roots, folder.Path)
	}
	source.Profiles = profiles
	var err error
	if source.Settings, err = client.MediaManagement(ctx); err != nil {
		return PreviewSource{}, fmt.Errorf("media management: %w", err)
	}
	return source, nil
}

// Source is one *Arr app's half of an import: what it lists, and how one listed item becomes Solarr's.
type Source[T any] struct {
	Connect  func(ctx context.Context) error
	List     func(ctx context.Context) ([]T, error)
	Describe func(item T) Item
	// Sync makes one item Solarr's in the library its root folder belongs to; an error still keeps its counts.
	Sync func(ctx context.Context, categoryID int64, item T) (library.ArrImportCounts, error)
	// Monitor sets what Solarr already holds of one item to the *Arr's monitored flag and adds nothing; nil means the app
	// has no monitoring-only import. categoryID is the library its root folder belongs to, 0 when none does.
	Monitor func(ctx context.Context, categoryID int64, item T) (library.ArrImportCounts, error)
	// Prepare runs once on the items a full import will sync, after the limit applies, so a bulk warm covers only those.
	Prepare func(ctx context.Context, chosen []T)
	// Workers is how many items are synced at once; each waits mostly on providers that pace themselves.
	Workers int
}

// Item is what the walk needs to know of a listed item.
type Item struct {
	Name, RootFolder string
	Monitored        bool
	// HasFiles says the *Arr holds a file of the item.
	HasFiles bool
}

// Walk syncs every item the options want, best-effort per item: one that fails or belongs to no library does not stop
// the rest. Failing to connect or to list is the one thing that fails the whole run.
func Walk[T any](ctx context.Context, run Run, source Source[T]) (library.ArrImportCounts, error) {
	if err := source.Connect(ctx); err != nil {
		return library.ArrImportCounts{}, fmt.Errorf("connect: %w", err)
	}
	all, err := source.List(ctx)
	if err != nil {
		return library.ArrImportCounts{}, fmt.Errorf("list: %w", err)
	}
	if run.Options.MonitoringOnly && source.Monitor == nil {
		return library.ArrImportCounts{}, errors.New("this app has no monitoring-only import")
	}
	var chosen []T
	for _, item := range all {
		described := source.Describe(item)
		monitored := described.Monitored
		// Monitoring only follows the flag both ways, so every item counts.
		if run.Options.MonitoringOnly || (monitored && run.Options.Monitored) || (!monitored && run.Options.Unmonitored) || (described.HasFiles && run.Options.Present) {
			chosen = append(chosen, item)
		}
	}
	if run.Options.Limit > 0 && len(chosen) > run.Options.Limit {
		chosen = chosen[:run.Options.Limit]
	}
	if source.Prepare != nil && !run.Options.MonitoringOnly {
		source.Prepare(ctx, chosen)
	}
	total := int64(len(chosen))
	run.Progress(0, total)

	var (
		mu       sync.Mutex
		walked   int64
		counts   library.ArrImportCounts
		failures []string
	)
	parallel.Do(ctx, len(chosen), max(1, source.Workers), func(ctx context.Context, index int) error {
		item := chosen[index]
		described := source.Describe(item)
		var got library.ArrImportCounts
		categoryID, placed := run.Place(described.RootFolder)
		err := fmt.Errorf("no category matches root folder %q", described.RootFolder)
		switch {
		case run.Options.MonitoringOnly:
			got, err = source.Monitor(ctx, categoryID, item)
		case placed:
			got, err = source.Sync(ctx, categoryID, item)
		}
		mu.Lock()
		defer mu.Unlock()
		counts = addCounts(counts, got)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", described.Name, err))
		}
		walked++
		run.Progress(walked, total)
		return nil
	})
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

// Tally is the counts of one first-kind item, created or matched.
func Tally(created bool, err error) (library.ArrImportCounts, error) {
	switch {
	case err != nil:
		return library.ArrImportCounts{}, err
	case created:
		return library.ArrImportCounts{ArtistsCreated: 1}, nil
	}
	return library.ArrImportCounts{ArtistsMatched: 1}, nil
}

// SameNameProfile is the Solarr profile of a media type named like an *Arr profile (a shipped movie profile's name
// leads with "Movie "), nil for none: the rule an import without a mapping follows.
func SameNameProfile(profiles []settings.QualityProfile, mediaType, name string) *int64 {
	if name == "" {
		return nil
	}
	for _, profile := range profiles {
		if profile.MediaType == mediaType && strings.EqualFold(strings.TrimPrefix(profile.Name, "Movie "), name) {
			id := profile.ID
			return &id
		}
	}
	return nil
}

// ProfileNames reads an instance's quality profile names; a failure leaves the map empty, so every item takes its
// library's profile.
func ProfileNames[P any](ctx context.Context, list func(context.Context) ([]P, error), id func(P) (int64, string)) map[int64]string {
	profiles, err := list(ctx)
	if err != nil {
		return nil
	}
	names := make(map[int64]string, len(profiles))
	for _, profile := range profiles {
		key, name := id(profile)
		names[key] = name
	}
	return names
}

// IDText is a numeric provider id as text, empty for none.
func IDText(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// LogWarm wraps a bulk warm of what an import is about to add: a failure only costs speed, each add still asks on its own.
func LogWarm(logger *slog.Logger, clocked clock.Clock, what string, warm func(context.Context, []string) error) func(context.Context, []string) {
	return func(ctx context.Context, ids []string) {
		started := clocked.Now()
		if err := warm(ctx, ids); err != nil {
			logger.WarnContext(ctx, "arr import: warming wikidata failed", slog.String("works", what), slog.Any("error", err))
			return
		}
		logger.InfoContext(ctx, "arr import: wikidata warmed", slog.String("works", what), slog.Int("ids", len(ids)),
			slog.Duration("took", clocked.Now().Sub(started)))
	}
}
