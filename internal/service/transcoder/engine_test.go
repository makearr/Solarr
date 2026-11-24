package transcoder

import (
	"errors"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

type fakeHardware map[transcode.Accel]transcode.EncoderSupport

func (f fakeHardware) EncoderFor(engine transcode.Accel, _ transcode.Codec) (transcode.EncoderSupport, bool) {
	row, found := f[engine]
	return row, found
}

func (f fakeHardware) BestEngine(_ transcode.Codec, tenBit bool) (transcode.Accel, bool) {
	for _, engine := range []transcode.Accel{transcode.NVENC, transcode.QSV, transcode.VAAPI} {
		if row, found := f[engine]; found && row.Working && (!tenBit || row.TenBit) {
			return engine, true
		}
	}
	return "", false
}

func TestResolveEngines(t *testing.T) {
	cpu := fakeHardware{transcode.Software: {Working: true}}
	gpu := fakeHardware{transcode.Software: {Working: true}, transcode.QSV: {Working: true, TenBit: false}, transcode.VAAPI: {Working: true, TenBit: true}}
	setup := func(engine settings.TranscodeEngine, fallback bool) settings.TranscodeSetup {
		return settings.TranscodeSetup{Encode: settings.TranscodeEncode{Engine: engine, AllowCPUFallback: fallback}}
	}
	cases := []struct {
		name     string
		hardware fakeHardware
		query    EngineQuery
		want     []transcode.Accel
		err      error
	}{
		{"auto without a GPU is software", cpu, EngineQuery{Setup: setup(settings.EngineAuto, false)}, []transcode.Accel{transcode.Software}, nil},
		{"auto takes the best GPU and keeps software only when allowed", gpu, EngineQuery{Setup: setup(settings.EngineAuto, false)}, []transcode.Accel{transcode.QSV}, nil},
		{"auto with the fallback ends in software", gpu, EngineQuery{Setup: setup(settings.EngineAuto, true)}, []transcode.Accel{transcode.QSV, transcode.Software}, nil},
		{"ten bit skips an engine without it", gpu, EngineQuery{Setup: setup(settings.EngineAuto, false), TenBit: true}, []transcode.Accel{transcode.VAAPI}, nil},
		{"a named engine that does not work falls back when allowed", cpu, EngineQuery{Setup: setup(settings.EngineQSV, true)}, []transcode.Accel{transcode.Software}, nil},
		{"a named engine that does not work fails when the fallback is off", cpu, EngineQuery{Setup: setup(settings.EngineQSV, false)}, nil, ErrEngineUnavailable},
		{"the person's engine beats the setup's", gpu, EngineQuery{Setup: setup(settings.EngineAuto, false), Requested: "software"}, []transcode.Accel{transcode.Software}, nil},
		{"software with no software encoder fails", fakeHardware{}, EngineQuery{Setup: setup(settings.EngineSoftware, true)}, nil, ErrEngineUnavailable},
	}
	for _, c := range cases {
		got, err := ResolveEngines(c.hardware, c.query)
		if !errors.Is(err, c.err) || !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, %v want %v, %v", c.name, got, err, c.want, c.err)
		}
	}
}
