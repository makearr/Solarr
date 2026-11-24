package transcoder

import "github.com/makearr/Solarr/internal/db/settings"

// Override is a person's retry settings for one item; zero fields keep the setup's.
type Override struct {
	Quality          float64 `json:"quality,omitempty"`
	VideoBitrateKbps int64   `json:"video_bitrate_kbps,omitempty"`
	Preset           string  `json:"preset,omitempty"`
	Engine           string  `json:"engine,omitempty"`
}

// PassSettings is what one pass of the ladder changes.
type PassSettings struct {
	Pass int
	// Quality is in software CRF terms; 0 takes the setup's.
	Quality float64
	// VideoBitrateKbps replaces the planned video bitrate when above 0.
	VideoBitrateKbps int64
	// Scale multiplies every encoded video and audio bitrate; 0 means 1.
	Scale  float64
	Preset string
	Engine string
}

// BitrateScale is Scale with the zero value read as no change.
func (p PassSettings) BitrateScale() float64 {
	if p.Scale <= 0 {
		return 1
	}
	return p.Scale
}

// FirstPass is the settings of a first attempt: the setup's, or the person's where they chose.
func FirstPass(setup settings.TranscodeSetup, override Override) PassSettings {
	pass := PassSettings{Quality: setup.Encode.Quality, Preset: string(setup.Encode.Preset), VideoBitrateKbps: override.VideoBitrateKbps, Engine: override.Engine}
	if override.Quality > 0 {
		pass.Quality = override.Quality
	}
	if override.Preset != "" {
		pass.Preset = override.Preset
	}
	return pass
}

func ladderable(code ReasonCode) bool {
	return code == ReasonResultLarger || code == ReasonResultAboveRange || code == ReasonResultAboveMax
}

// Ladderable says every failure is one a lower quality can cure.
func Ladderable(failures []Reason) bool {
	if len(failures) == 0 {
		return false
	}
	for _, failure := range failures {
		if !ladderable(failure.Code) {
			return false
		}
	}
	return true
}

// NextPass lowers the quality or the bitrate for another try; false when the failures are not about size, nothing is
// re-encoded, the passes are used up, or the setup's step would change nothing.
func NextPass(setup settings.TranscodeSetup, plan Plan, current PassSettings, failures []Reason) (PassSettings, bool) {
	ladder := setup.Ladder
	if !Ladderable(failures) || (!plan.EncodesVideo && !plan.EncodesAudio) || int64(current.Pass) >= ladder.ExtraPasses {
		return PassSettings{}, false
	}
	next := current
	next.Pass++
	byQuality := setup.Encode.RateMode == settings.RateQuality && plan.EncodesVideo
	switch {
	case byQuality && ladder.QualityStep > 0:
		next.Quality = current.Quality + ladder.QualityStep
	case !byQuality && ladder.BitrateStepPct > 0 && ladder.BitrateStepPct < 100 && encodesLossy(plan):
		next.Scale = current.BitrateScale() * (1 - float64(ladder.BitrateStepPct)/100)
	default:
		return PassSettings{}, false
	}
	return next, true
}

// encodesLossy says some encoded stream has a bitrate to lower: lossless audio has none.
func encodesLossy(plan Plan) bool {
	for _, group := range [][]StreamPlan{plan.Video, plan.Audio} {
		for _, stream := range group {
			if stream.Action == ActionEncode && (stream.BitrateKbps > 0 || stream.Type == "video") {
				return true
			}
		}
	}
	return false
}
