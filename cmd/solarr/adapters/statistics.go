package adapters

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

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
	return view, nil
}
