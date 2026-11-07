package settings_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestSolarrMode_WhatEachModeAllows(t *testing.T) {
	type allowed struct{ readOnly, paused, searchesStopped, pushesUnasked, pushesAsked bool }
	cases := map[settings.SolarrMode]allowed{
		settings.ModeNormal:         {false, false, false, false, true},
		settings.ModeReadOnly:       {true, false, false, false, true},
		settings.ModePauseDownloads: {false, true, false, false, true},
		settings.ModeObserve:        {true, true, true, false, false},
		settings.ModeArrCompanion:   {true, true, true, true, true},
	}
	for mode, want := range cases {
		got := allowed{mode.LibrariesReadOnly(), mode.DownloadsPaused(), mode.SearchesStopped(), mode.PushesMonitoring(false), mode.PushesMonitoring(true)}
		if got != want {
			t.Errorf("%s = %+v, want %+v", mode, got, want)
		}
	}
}
