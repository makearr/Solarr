package adapters

import (
	"context"
	"sort"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// IncompleteDiscographies satisfies apiserver.IncompleteDiscographies: the rollup's rows, named from the people store.
type IncompleteDiscographies struct {
	Statistics library.Statistics
	People     library.People
}

func (d IncompleteDiscographies) IncompleteDiscographies(ctx context.Context, categoryID int64, limit int) ([]apiserver.IncompleteDiscographyRow, error) {
	rows, err := d.Statistics.IncompleteDiscographies(ctx, categoryID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.IncompleteDiscographyRow, 0, len(rows))
	for _, row := range rows {
		person, err := d.People.Person(ctx, row.PersonID)
		if err != nil {
			continue
		}
		out = append(out, apiserver.IncompleteDiscographyRow{
			PersonID: row.PersonID, Name: person.Name, Held: row.Held, Total: row.Total, Monitored: person.Monitored == 1,
		})
	}
	return out, nil
}

// Statistics satisfies apiserver.StatisticsReader over library.Statistics.
type Statistics struct{ Statistics library.Statistics }

func (s Statistics) CategoryStatistics(ctx context.Context, categoryID int64) (apiserver.StatisticsView, error) {
	view := apiserver.StatisticsView{WindowDays: library.StatisticsWindowDays}
	for _, metric := range []struct {
		name string
		into *[]apiserver.StatisticsFigure
	}{
		{library.StatFilesByCodec, &view.FilesByCodec},
		{library.StatAlbumsByState, &view.AlbumsByState},
		{library.StatDownloadsByState, &view.DownloadsByState},
		{library.StatGrowthByMonth, &view.GrowthByMonth},
		{library.StatDecisionsByOutcome, &view.DecisionsByOutcome},
	} {
		figures, err := s.Statistics.ForCategory(ctx, metric.name, categoryID)
		if err != nil {
			return apiserver.StatisticsView{}, err
		}
		for _, figure := range figures {
			*metric.into = append(*metric.into, apiserver.StatisticsFigure{Key: figure.Key, Value: figure.Value, Count: figure.Count})
			if at := time.Unix(figure.ComputedAt, 0); at.After(view.ComputedAt) {
				view.ComputedAt = at
			}
		}
	}
	sort.Slice(view.GrowthByMonth, func(i, j int) bool { return view.GrowthByMonth[i].Key < view.GrowthByMonth[j].Key })
	return view, nil
}
