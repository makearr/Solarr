package transcoder

import (
	"errors"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// ErrEngineUnavailable is no engine the setup allows can encode the target's codec here.
var ErrEngineUnavailable = errors.New("transcoder: no working encoder for the target's codec")

// Hardware is the encoder matrix of this machine.
type Hardware interface {
	EncoderFor(engine transcode.Accel, codec transcode.Codec) (transcode.EncoderSupport, bool)
	BestEngine(codec transcode.Codec, tenBit bool) (transcode.Accel, bool)
}

// EngineQuery is what the choice of engines depends on.
type EngineQuery struct {
	Setup settings.TranscodeSetup
	// Requested is a person's engine for one file; empty follows the setup.
	Requested string
	Codec     transcode.Codec
	TenBit    bool
}

// ResolveEngines lists the engines to try for a video encode, first choice first; software, when allowed, comes last.
func ResolveEngines(hardware Hardware, query EngineQuery) ([]transcode.Accel, error) {
	chosen := settings.TranscodeEngine(query.Requested)
	if chosen == "" {
		chosen = query.Setup.Encode.Engine
	}
	working := func(engine transcode.Accel) bool {
		row, found := hardware.EncoderFor(engine, query.Codec)
		return found && row.Working && (!query.TenBit || row.TenBit || engine == transcode.Software)
	}
	softwareOK := working(transcode.Software)
	var engines []transcode.Accel
	// withSoftware says software may follow: always when it is the choice or nothing else works, else only when the setup allows the fallback.
	withSoftware := query.Setup.Encode.AllowCPUFallback
	switch chosen {
	case settings.EngineSoftware:
		withSoftware = true
	case settings.EngineAuto:
		if best, found := hardware.BestEngine(query.Codec, query.TenBit); found {
			engines = append(engines, best)
		} else {
			withSoftware = true
		}
	default:
		if engine := transcode.Accel(chosen); working(engine) {
			engines = append(engines, engine)
		}
	}
	if withSoftware && softwareOK {
		engines = append(engines, transcode.Software)
	}
	if len(engines) == 0 {
		return nil, ErrEngineUnavailable
	}
	return engines, nil
}
