package app

import (
	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// tvRefreshJob is the series refresh sweep: new episodes, seasons, air times and credits without a click.
func (p *process) tvRefreshJob() media.JobRegistration {
	return media.JobRegistration{Definition: tv.RefreshDefinition, Handler: tv.NewRefreshJob(tv.RefreshConfig{
		Reader: library.NewSeriesReader(p.library), Service: p.tvService(), DefaultProfileID: p.defaultQualityProfileID,
		Categories: adapters.Categories{Cache: p.cache}, Clock: clock.NewSystem(), Logger: p.logger,
	})}
}
