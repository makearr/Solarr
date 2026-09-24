package app

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestRescanAfterRefresh_FollowsTheLibrarysChoice(t *testing.T) {
	for _, tc := range []struct {
		choice settings.RescanAfterRefresh
		manual bool
		want   bool
	}{
		{settings.RescanAlways, false, true},
		{settings.RescanAfterManual, false, false},
		{settings.RescanAfterManual, true, true},
		{settings.RescanNever, true, false},
	} {
		if got := rescanAfterRefreshChoice(tc.choice, tc.manual); got != tc.want {
			t.Errorf("%s, manual %v: %v, want %v", tc.choice, tc.manual, got, tc.want)
		}
	}
}
