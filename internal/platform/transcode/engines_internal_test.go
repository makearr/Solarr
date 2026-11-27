package transcode

import (
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func memWith(t *testing.T, directories ...string) *fsx.Mem {
	t.Helper()
	memory := fsx.NewMem(clock.NewFakeAtEpoch())
	for _, directory := range directories {
		if err := memory.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return memory
}

func TestSysfsEngines_CountsTheKernelsVideoEnginesAndAnswersUnknownForNone(t *testing.T) {
	intel := memWith(t,
		"/sys/class/drm/renderD128/device/drm/card1/engine/rcs0", "/sys/class/drm/renderD128/device/drm/card1/engine/vcs0",
		"/sys/class/drm/renderD128/device/drm/card1/engine/vcs1", "/sys/class/drm/renderD128/device/drm/card1/engine/vecs0",
		"/sys/class/drm/renderD128/device/drm/card1-DP-1/engine/vcs9")
	if got := sysfsEngines(intel, "renderD128"); got != (EngineCounts{Encode: 2, Decode: 2}) {
		t.Errorf("Intel = %+v, want 2 and 2", got)
	}
	amd := memWith(t, "/sys/class/drm/renderD129/device/ip_discovery/die/0/VCN/0", "/sys/class/drm/renderD129/device/ip_discovery/die/0/VCN/1", "/sys/class/drm/renderD129/device/ip_discovery/die/0/GC/0")
	if got := sysfsEngines(amd, "renderD129"); got != (EngineCounts{Encode: 2, Decode: 2}) {
		t.Errorf("AMD = %+v, want 2 and 2", got)
	}
	if got := sysfsEngines(memWith(t, "/sys/class/drm/renderD130/device"), "renderD130"); got != (EngineCounts{}) {
		t.Errorf("no engine list = %+v, want unknown", got)
	}
}
