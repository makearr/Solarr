package app

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// libraryGuard refuses writes under a root folder while the mode keeps libraries read-only; it allows everything
// until the settings cache is bound, which is before any library is touched.
type libraryGuard struct {
	cache atomic.Pointer[settings.Cache]
}

func (g *libraryGuard) deny(path string) error {
	cache := g.cache.Load()
	if cache == nil {
		return nil
	}
	snapshot := cache.Snapshot()
	if !snapshot.SolarrMode().LibrariesReadOnly() {
		return nil
	}
	for _, root := range snapshot.RootFolders() {
		if fsx.Within(root.Path, path) {
			return fsx.ErrReadOnly
		}
	}
	return nil
}

// solarrMode is the mode in force now.
func (p *process) solarrMode() settings.SolarrMode { return p.cache.Snapshot().SolarrMode() }

// applyModeEnv makes SOLARR_MODE, when set, the stored mode: a variable that is set wins, as every environment setting does.
func (p *process) applyModeEnv(ctx context.Context) error {
	if !config.EnvSet("mode", p.env) {
		return nil
	}
	mode := settings.SolarrMode(p.config.Mode)
	if p.solarrMode() == mode {
		return nil
	}
	if err := p.cache.SetSolarrMode(ctx, clock.NewSystem(), mode); err != nil {
		return fmt.Errorf("%s: %w", config.EnvName("mode"), err)
	}
	p.logger.Info("mode set from the environment", "mode", string(mode))
	return nil
}
