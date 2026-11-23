package transcoder

// Verdict is whether a file needs converting.
type Verdict string

// The verdicts; blocked is a file that cannot be converted without losing something Solarr will not lose.
const (
	Needed    Verdict = "needed"
	NotNeeded Verdict = "not_needed"
	Blocked   Verdict = "blocked"
)

// ReasonCode names why a rule fired; the web interface turns a code and its values into text.
type ReasonCode string

// Skip rules that failed (a file needs converting), or passed with a note.
const (
	ReasonTranscodedBefore ReasonCode = "transcoded_before"
	ReasonContainer        ReasonCode = "container"
	ReasonVideoCodec       ReasonCode = "video_codec"
	ReasonAudioCodec       ReasonCode = "audio_codec"
	ReasonResolution       ReasonCode = "resolution"
	ReasonHDR              ReasonCode = "hdr"
	ReasonFrameRate        ReasonCode = "frame_rate"
	ReasonSampleRate       ReasonCode = "sample_rate"
	ReasonChannels         ReasonCode = "channels"
	ReasonUnwantedAudio    ReasonCode = "unwanted_audio_language"
	ReasonUnwantedSubtitle ReasonCode = "unwanted_subtitle_language"
	ReasonAboveRange       ReasonCode = "above_range"
	ReasonAboveMax         ReasonCode = "above_max"
	ReasonBelowRange       ReasonCode = "below_range"
	ReasonRangeUnknown     ReasonCode = "range_unknown"
	ReasonNothingToDo      ReasonCode = "nothing_to_do"
)

// Reasons a file is blocked.
const (
	ReasonDolbyVisionProfile5 ReasonCode = "dolby_vision_profile5"
	ReasonLinkedCopies        ReasonCode = "linked_copies"
	ReasonNoStreams           ReasonCode = "no_streams"
	ReasonHDRMetadata         ReasonCode = "hdr_metadata"
	ReasonUnsupportedTarget   ReasonCode = "unsupported_target"
)

// Reasons a stream is dropped.
const (
	ReasonDataStream        ReasonCode = "data_stream"
	ReasonUnknownStream     ReasonCode = "unknown_stream"
	ReasonAttachment        ReasonCode = "attachment"
	ReasonExtraVideoStream  ReasonCode = "extra_video_stream"
	ReasonPictureSubtitle   ReasonCode = "picture_subtitle"
	ReasonDuplicateLanguage ReasonCode = "duplicate_language"
	ReasonCommentary        ReasonCode = "commentary"
	ReasonContainerCannot   ReasonCode = "container_cannot_hold"
)

// Reason is structured, never a formatted string; Stream is -1 for the file.
type Reason struct {
	Code   ReasonCode `json:"code"`
	Stream int        `json:"stream"`
	Want   string     `json:"want,omitempty"`
	Have   string     `json:"have,omitempty"`
}
