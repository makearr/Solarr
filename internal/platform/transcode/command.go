package transcode

import (
	"fmt"
	"strconv"
)

// Kind is what the output carries.
type Kind string

// The two outputs: video with sound, and sound alone.
const (
	Video Kind = "video"
	Audio Kind = "audio"
)

// Spec is one conversion.
type Spec struct {
	Kind        Kind
	Path        string
	StartSecond float64
	Accel       Accel
	Device      string
	MaxHeight   int64
	VideoKbps   int64
	// HardwareDecode keeps frames on the GPU from decoding to encoding; the codecs the GPU cannot decode need it off.
	HardwareDecode bool
}

// ContentType is what the output is, for the response header.
func (s Spec) ContentType() string {
	if s.Kind == Audio {
		return "audio/mpeg"
	}
	return "video/mp4"
}

const (
	audioKbps = 160
	gopFrames = 48
)

// Args builds the ffmpeg command line. With HardwareDecode the whole path runs on the GPU; without it only
// encoding does, which accepts every source codec and bit depth that hardware decode does not.
func Args(spec Spec) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if spec.Kind == Video {
		args = append(args, deviceArgs(spec)...)
	}
	if spec.StartSecond > 0 {
		args = append(args, "-ss", strconv.FormatFloat(spec.StartSecond, 'f', 3, 64))
	}
	args = append(args, "-i", spec.Path)

	if spec.Kind == Audio {
		return append(args, "-vn", "-map", "0:a:0", "-c:a", "libmp3lame", "-b:a", "192k", "-f", "mp3", "pipe:1")
	}

	kbps := strconv.FormatInt(spec.VideoKbps, 10) + "k"
	args = append(args, "-map", "0:v:0", "-map", "0:a:0?", "-vf", videoFilter(spec))
	args = append(args, encoderArgs(spec.Accel)...)
	args = append(args, "-b:v", kbps, "-maxrate", kbps, "-bufsize", strconv.FormatInt(spec.VideoKbps*2, 10)+"k",
		"-g", strconv.Itoa(gopFrames),
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", audioKbps), "-ac", "2",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
	return args
}

// CanDecodeOnGPU says whether Args has a full-GPU path for the encoder.
func CanDecodeOnGPU(accel Accel) bool { return accel == VAAPI || accel == NVENC }

func deviceArgs(spec Spec) []string {
	if spec.HardwareDecode {
		switch spec.Accel {
		case VAAPI:
			return []string{"-hwaccel", "vaapi", "-hwaccel_device", spec.Device, "-hwaccel_output_format", "vaapi"}
		case NVENC:
			return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
		}
	}
	switch spec.Accel {
	case VAAPI:
		return []string{"-init_hw_device", "vaapi=hw:" + spec.Device, "-filter_hw_device", "hw"}
	case QSV:
		return []string{"-init_hw_device", "vaapi=va:" + spec.Device, "-init_hw_device", "qsv=hw@va", "-filter_hw_device", "hw"}
	}
	return nil
}

// videoFilter scales down to the height cap (none: the source size) on an even size and puts frames where the encoder reads them.
func videoFilter(spec Spec) string {
	// No cap keeps the source's size, only evened out: encoders refuse odd dimensions.
	height := "'trunc(ih/2)*2'"
	if spec.MaxHeight > 0 {
		height = fmt.Sprintf("'trunc(min(ih,%d)/2)*2'", spec.MaxHeight)
	}
	if spec.HardwareDecode {
		switch spec.Accel {
		case VAAPI:
			return "scale_vaapi=w=-2:h=" + height + ":format=nv12"
		case NVENC:
			return "scale_cuda=w=-2:h=" + height
		}
	}
	scale := "scale=-2:" + height
	switch spec.Accel {
	case VAAPI:
		return scale + ",format=nv12,hwupload"
	case QSV:
		return scale + ",format=nv12,hwupload=extra_hw_frames=64"
	}
	return scale + ",format=yuv420p"
}

func encoderArgs(accel Accel) []string {
	switch accel {
	case VAAPI:
		return []string{"-c:v", "h264_vaapi"}
	case QSV:
		return []string{"-c:v", "h264_qsv", "-preset", "veryfast"}
	case NVENC:
		return []string{"-c:v", "h264_nvenc", "-preset", "p4", "-tune", "ll"}
	}
	return []string{"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high"}
}
