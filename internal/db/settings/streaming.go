package settings

import (
	"context"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// The streaming keys: the preview player's server-side transcode (documentation/media-player.md).
const (
	KeyStreamingTranscodeEnabled = "streaming.transcode_enabled"
	KeyStreamingHardware         = "streaming.hardware"
	KeyStreamingDevice           = "streaming.device"
	KeyStreamingMaxHeight        = "streaming.max_height"
	KeyStreamingVideoKbps        = "streaming.video_kbps"
	KeyStreamingMaxSessions      = "streaming.max_sessions"
)

// The hardware choices. Auto takes the best encoder detection found working, Software never uses a GPU.
const (
	StreamingHardwareAuto     = "auto"
	StreamingHardwareSoftware = "software"
	StreamingHardwareVAAPI    = "vaapi"
	StreamingHardwareQSV      = "qsv"
	StreamingHardwareNVENC    = "nvenc"
)

// StreamingHardwareChoices is every value streaming.hardware accepts.
var StreamingHardwareChoices = []string{
	StreamingHardwareAuto, StreamingHardwareSoftware, StreamingHardwareVAAPI, StreamingHardwareQSV, StreamingHardwareNVENC,
}

// Streaming defaults: no height cap (the original size), 6 Mbit/s when the source bitrate is unknown.
const (
	DefaultStreamingMaxHeight   = 0
	DefaultStreamingVideoKbps   = 6000
	DefaultStreamingMaxSessions = 2
)

// StreamingSettings is the player's transcode policy.
type StreamingSettings struct {
	TranscodeEnabled bool
	Hardware         string
	// Device is a render node such as /dev/dri/renderD128; empty takes the one detection found.
	Device      string
	MaxHeight   int64
	VideoKbps   int64
	MaxSessions int64
}

// StreamingSettings reads the policy, each missing key at its default.
func (s *Snapshot) StreamingSettings() StreamingSettings {
	hardware := s.policy[KeyStreamingHardware]
	if !slices.Contains(StreamingHardwareChoices, hardware) {
		hardware = StreamingHardwareAuto
	}
	return StreamingSettings{
		TranscodeEnabled: s.policyBool(KeyStreamingTranscodeEnabled, true),
		Hardware:         hardware,
		Device:           s.policy[KeyStreamingDevice],
		MaxHeight:        s.policyInt(KeyStreamingMaxHeight, DefaultStreamingMaxHeight),
		VideoKbps:        s.policyInt(KeyStreamingVideoKbps, DefaultStreamingVideoKbps),
		MaxSessions:      s.policyInt(KeyStreamingMaxSessions, DefaultStreamingMaxSessions),
	}
}

// SetStreamingSettings validates and writes the whole policy in one transaction (ADR-44).
func (c *Cache) SetStreamingSettings(ctx context.Context, cl clock.Clock, streaming StreamingSettings) error {
	if problems := validateStreamingSettings(streaming); len(problems) > 0 {
		return problems
	}
	return c.setPolicies(ctx, cl, map[string]string{
		KeyStreamingTranscodeEnabled: boolPolicy(streaming.TranscodeEnabled),
		KeyStreamingHardware:         streaming.Hardware,
		KeyStreamingDevice:           streaming.Device,
		KeyStreamingMaxHeight:        strconv.FormatInt(streaming.MaxHeight, 10),
		KeyStreamingVideoKbps:        strconv.FormatInt(streaming.VideoKbps, 10),
		KeyStreamingMaxSessions:      strconv.FormatInt(streaming.MaxSessions, 10),
	})
}

func validateStreamingSettings(streaming StreamingSettings) config.Problems {
	var problems config.Problems
	if !slices.Contains(StreamingHardwareChoices, streaming.Hardware) {
		problems = append(problems, config.Problem{
			Path: KeyStreamingHardware, Value: streaming.Hardware, Expectation: "one of auto, software, vaapi, qsv, nvenc",
		})
	}
	if streaming.MaxHeight != 0 && (streaming.MaxHeight < 144 || streaming.MaxHeight > 4320) {
		problems = append(problems, config.Problem{
			Path: KeyStreamingMaxHeight, Value: strconv.FormatInt(streaming.MaxHeight, 10), Expectation: "0 for no cap, or a height between 144 and 4320",
		})
	}
	if streaming.VideoKbps < 200 || streaming.VideoKbps > 100000 {
		problems = append(problems, config.Problem{
			Path: KeyStreamingVideoKbps, Value: strconv.FormatInt(streaming.VideoKbps, 10), Expectation: "a bitrate between 200 and 100000 kbit/s",
		})
	}
	if streaming.MaxSessions < 1 || streaming.MaxSessions > 32 {
		problems = append(problems, config.Problem{
			Path: KeyStreamingMaxSessions, Value: strconv.FormatInt(streaming.MaxSessions, 10), Expectation: "between 1 and 32 sessions",
		})
	}
	return problems
}
