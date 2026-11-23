package comic

import (
	"context"
	"errors"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/media"
)

// ManagerImport adds the series of a comic manager (Kapowarr or Mylar3, whichever App names), four at once, each by its
// ComicVine volume id. A series Solarr already holds is matched and left as it is. Series count in the run's first-kind
// tally; a monitoring-only run sets the flags of what is held.
type ManagerImport struct {
	App      settings.ArrApp
	Adding   media.Adding
	Monitors arrmap.Monitors
}

func (m ManagerImport) list(ctx context.Context, client arrimport.LidarrClient) ([]arrimport.ComicSeries, error) {
	if m.App == settings.ArrAppKapowarr {
		return client.Kapowarr().Volumes(ctx)
	}
	return client.Mylar().Comics(ctx)
}

func (m ManagerImport) connect(ctx context.Context, client arrimport.LidarrClient) error {
	var err error
	if m.App == settings.ArrAppKapowarr {
		_, err = client.Kapowarr().SystemStatus(ctx)
	} else {
		_, err = client.Mylar().SystemStatus(ctx)
	}
	return err
}

// Import walks the manager's series.
func (m ManagerImport) Import(ctx context.Context, run arrmap.Run) (library.ArrImportCounts, error) {
	return arrmap.Walk(ctx, run, arrmap.Source[arrimport.ComicSeries]{
		Connect: func(ctx context.Context) error {
			if m.Adding == nil {
				return errors.New("no comic service")
			}
			return m.connect(ctx, run.Client)
		},
		List: func(ctx context.Context) ([]arrimport.ComicSeries, error) { return m.list(ctx, run.Client) },
		Describe: func(series arrimport.ComicSeries) arrmap.Item {
			return arrmap.Item{Name: series.Title, RootFolder: series.RootFolder, Monitored: series.Monitored, HasFiles: series.HasFiles}
		},
		Workers: 4,
		Monitor: func(ctx context.Context, categoryID int64, series arrimport.ComicSeries) (library.ArrImportCounts, error) {
			if series.ComicVine == "" {
				return library.ArrImportCounts{}, nil
			}
			_, held, err := m.Monitors.Work(ctx, categoryID, MediaType, ProviderComicVine, series.ComicVine, series.Monitored)
			return arrmap.Found(held, err)
		},
		Sync: func(ctx context.Context, categoryID int64, series arrimport.ComicSeries) (library.ArrImportCounts, error) {
			return arrmap.Tally(m.syncSeries(ctx, run, categoryID, series))
		},
	})
}

func (m ManagerImport) syncSeries(ctx context.Context, run arrmap.Run, categoryID int64, series arrimport.ComicSeries) (bool, error) {
	if series.ComicVine == "" {
		return false, errors.New("no ComicVine id")
	}
	result, err := m.Adding.AddWork(ctx, media.AddRequest{
		CategoryID: categoryID, Kind: AddKindSeries, Provider: ProviderComicVine, ExternalID: series.ComicVine, Monitored: series.Monitored,
		Fallback: &media.Fallback{Title: series.Title, Year: series.Year, ExternalIDs: map[string]string{ProviderComicVine: series.ComicVine}},
	})
	if err != nil {
		return false, err
	}
	workID := result.WorkID
	if err := run.Record(ctx, library.NewArrImportItem{
		Kind: library.ArrImportItemSeries, SourceID: series.ID, Title: series.Title, WorkID: &workID, WorkCreated: result.Created,
	}); err != nil {
		return false, err
	}
	return result.Created, nil
}

// Preview reads the manager's series; Mylar3 has no root folders, so the folders its series live in stand for them.
func (m ManagerImport) Preview(ctx context.Context, client arrimport.LidarrClient) (arrmap.PreviewSource, error) {
	series, err := m.list(ctx, client)
	if err != nil {
		return arrmap.PreviewSource{}, err
	}
	source := arrmap.PreviewSource{ItemKind: "series", MediaType: MediaType}
	seen := map[string]bool{}
	for _, one := range series {
		source.Items = append(source.Items, arrmap.PreviewItem{
			RootFolder: one.RootFolder, Monitored: one.Monitored, Provider: ProviderComicVine, ExternalID: one.ComicVine,
		})
		if one.RootFolder != "" && !seen[one.RootFolder] {
			seen[one.RootFolder] = true
			source.Roots = append(source.Roots, one.RootFolder)
		}
	}
	return source, nil
}
