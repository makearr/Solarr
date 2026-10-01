package score

import "strings"

// containerOfName maps a file extension, or ffprobe's demuxer list, to the container format it names, so a
// profile that says "m4a" and one that says "mp4" mean the same container (M1g 1.7).
var containerOfName = map[string]string{
	"m4a": "mp4", "m4b": "mp4", "mp4": "mp4", "mov": "mp4",
	"mp3": "mp3", "flac": "flac", "wav": "wav", "wave": "wav",
	"ogg": "ogg", "oga": "ogg", "opus": "ogg",
	"wma": "asf", "asf": "asf",
	"mkv": "matroska", "mka": "matroska", "webm": "matroska", "matroska": "matroska",
	"aac": "aac", "ape": "ape", "wv": "wavpack",
}

// CanonicalContainer is the container a name stands for; an unknown name is itself, lower-cased.
func CanonicalContainer(name string) string {
	lowered := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, ".")))
	if container, known := containerOfName[lowered]; known {
		return container
	}
	// ffprobe lists the demuxer's names, "mov,mp4,m4a,3gp,3g2,mj2": the first one it knows decides.
	for _, part := range strings.Split(lowered, ",") {
		if container, known := containerOfName[part]; known {
			return container
		}
	}
	return lowered
}

// canonicalFor is how a dimension's tier entry and fact are compared: containers by format, the rest as written.
func canonicalFor(dimension, value string) string {
	if dimension == "container" {
		return CanonicalContainer(value)
	}
	return value
}
