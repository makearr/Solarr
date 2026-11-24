package transcoder

import "github.com/makearr/Solarr/internal/platform/transcode"

// Action is what happens to one stream.
type Action string

// The actions: copy keeps the bytes, encode makes new ones, convert changes a subtitle's format, drop leaves it out.
const (
	ActionCopy    Action = "copy"
	ActionEncode  Action = "encode"
	ActionConvert Action = "convert"
	ActionDrop    Action = "drop"
)

// HDRAction is what happens to a video stream's high dynamic range.
type HDRAction string

// The HDR actions.
const (
	HDRNone    HDRAction = ""
	HDRKeep    HDRAction = "keep"
	HDRTonemap HDRAction = "tonemap"
)

// StreamPlan is what becomes of one input stream; it is stored as plan_json, so its names are stable.
type StreamPlan struct {
	Index       int                  `json:"index"`
	Type        transcode.StreamType `json:"type"`
	Action      Action               `json:"action"`
	Reason      ReasonCode           `json:"reason,omitempty"`
	Codec       string               `json:"codec,omitempty"`
	Width       int                  `json:"width,omitempty"`
	Height      int                  `json:"height,omitempty"`
	FPS         float64              `json:"fps,omitempty"`
	BitDepth    int                  `json:"bit_depth,omitempty"`
	Channels    int                  `json:"channels,omitempty"`
	SampleRate  int                  `json:"sample_rate,omitempty"`
	BitrateKbps int64                `json:"bitrate_kbps,omitempty"`
	Language    string               `json:"language,omitempty"`
	Title       string               `json:"title,omitempty"`
	Default     bool                 `json:"default,omitempty"`
	Forced      bool                 `json:"forced,omitempty"`
	HDR         HDRAction            `json:"hdr,omitempty"`
}

// Plan is what converting one file would do.
type Plan struct {
	Verdict   Verdict  `json:"verdict"`
	Reasons   []Reason `json:"reasons,omitempty"`
	Warnings  []Reason `json:"warnings,omitempty"`
	Container string   `json:"container,omitempty"`
	// Remux is true when no stream is re-encoded.
	Remux        bool         `json:"remux,omitempty"`
	Video        []StreamPlan `json:"video,omitempty"`
	Audio        []StreamPlan `json:"audio,omitempty"`
	Subtitles    []StreamPlan `json:"subtitles,omitempty"`
	Other        []StreamPlan `json:"other,omitempty"`
	Dropped      []StreamPlan `json:"dropped,omitempty"`
	EncodesVideo bool         `json:"encodes_video,omitempty"`
	EncodesAudio bool         `json:"encodes_audio,omitempty"`
}

// Lane is the pool of workers a conversion takes a place in.
type Lane string

// The lanes: audio is every conversion that encodes no video.
const (
	LaneAudio Lane = "audio"
	LaneGPU   Lane = "gpu"
	LaneCPU   Lane = "cpu"
)

// Lane is the lane of the plan when the video, if any, is encoded by the engine.
func (p Plan) Lane(engine transcode.Accel) Lane {
	switch {
	case !p.EncodesVideo:
		return LaneAudio
	case engine == transcode.Software:
		return LaneCPU
	}
	return LaneGPU
}

// tenBitVideo says an encoded video stream is 10 bit, which a GPU engine must support too.
func (p Plan) tenBitVideo() bool {
	for _, stream := range p.Video {
		if stream.Action == ActionEncode && stream.BitDepth >= 10 {
			return true
		}
	}
	return false
}
