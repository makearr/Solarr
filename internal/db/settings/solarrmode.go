package settings

import (
	"context"
	"slices"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// KeySolarrMode is how much of the outside world this instance touches (documentation/solarr-mode.md).
const KeySolarrMode = "general.solarr_mode"

// SolarrMode is the instance-wide level of what Solarr is allowed to change.
type SolarrMode string

// The modes, from everything allowed to the most restrained.
const (
	// ModeNormal does everything it is configured to.
	ModeNormal SolarrMode = "normal"
	// ModeReadOnly never writes into a library folder; searching and downloading go on.
	ModeReadOnly SolarrMode = "read_only"
	// ModePauseDownloads sends nothing to a download client; libraries are still managed.
	ModePauseDownloads SolarrMode = "pause_downloads"
	// ModeObserve is read-only, with no indexer search and no download.
	ModeObserve SolarrMode = "observe"
	// ModeArrCompanion is observe, plus sending monitoring back to the *Arrs.
	ModeArrCompanion SolarrMode = "arr_companion"
)

// SolarrModeChoices is every value the setting accepts.
var SolarrModeChoices = []SolarrMode{ModeNormal, ModeReadOnly, ModePauseDownloads, ModeObserve, ModeArrCompanion}

// LibrariesReadOnly is whether library folders are off limits for writing.
func (m SolarrMode) LibrariesReadOnly() bool {
	return m == ModeReadOnly || m == ModeObserve || m == ModeArrCompanion
}

// DownloadsPaused is whether nothing may be sent to a download client.
func (m SolarrMode) DownloadsPaused() bool {
	return m == ModePauseDownloads || m == ModeObserve || m == ModeArrCompanion
}

// SearchesStopped is whether indexers are left alone.
func (m SolarrMode) SearchesStopped() bool { return m == ModeObserve || m == ModeArrCompanion }

// PushesMonitoring is whether monitoring goes back to the *Arrs: every connection in a companion, none in observe,
// and the ones that asked for it otherwise.
func (m SolarrMode) PushesMonitoring(connectionAsked bool) bool {
	switch m {
	case ModeArrCompanion:
		return true
	case ModeObserve:
		return false
	default:
		return connectionAsked
	}
}

// SolarrMode is the stored mode; anything unreadable is normal.
func (s *Snapshot) SolarrMode() SolarrMode {
	mode := SolarrMode(s.policy[KeySolarrMode])
	if !slices.Contains(SolarrModeChoices, mode) {
		return ModeNormal
	}
	return mode
}

// SetSolarrMode stores the mode.
func (c *Cache) SetSolarrMode(ctx context.Context, cl clock.Clock, mode SolarrMode) error {
	if !slices.Contains(SolarrModeChoices, mode) {
		return config.Problems{{Path: KeySolarrMode, Value: string(mode), Expectation: "one of normal, read_only, pause_downloads, observe, arr_companion"}}
	}
	return c.setPolicies(ctx, cl, map[string]string{KeySolarrMode: string(mode)})
}
