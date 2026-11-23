package transcode

import "strconv"

// Rate modes a conversion spends video bits by.
const (
	RateModeQuality = "quality"
	RateModeBitrate = "bitrate"
)

// RateControl is how one encoder is told to spend bits; Quality is already in the encoder's own scale.
type RateControl struct {
	Mode        string
	Quality     float64
	BitrateKbps int64
	// Lookahead adds the encoder's lookahead arguments; detection leaves it off for an encoder that refused them.
	Lookahead bool
	// QualityMode picks between VAAPI's "icq" and "cqp".
	QualityMode string
}

// RateArgs are the rate-control arguments of one encoder (documentation/transcode.md); unknown encoders get none.
func RateArgs(encoder string, rate RateControl) []string {
	quality := strconv.FormatFloat(rate.Quality, 'f', -1, 64)
	bitrate := strconv.FormatInt(rate.BitrateKbps, 10) + "k"
	peak := strconv.FormatInt(rate.BitrateKbps*3/2, 10) + "k"
	buffer := strconv.FormatInt(rate.BitrateKbps*3, 10) + "k"
	byQuality := rate.Mode != RateModeBitrate
	lookahead := func(args ...string) []string {
		if rate.Lookahead {
			return args
		}
		return nil
	}
	join := func(parts ...[]string) []string {
		var out []string
		for _, part := range parts {
			out = append(out, part...)
		}
		return out
	}
	switch encoder {
	case "libx264", "libx265":
		params := "-x264-params"
		look := "rc-lookahead=40"
		if encoder == "libx265" {
			params, look = "-x265-params", "rc-lookahead=40:log-level=error"
			if !rate.Lookahead {
				look = "log-level=error"
			}
		} else if !rate.Lookahead {
			params = ""
		}
		tail := []string{}
		if params != "" {
			tail = []string{params, look}
		}
		if byQuality {
			return join([]string{"-crf", quality}, tail)
		}
		return join([]string{"-b:v", bitrate, "-maxrate", peak, "-bufsize", buffer}, tail)
	case "libsvtav1":
		if byQuality {
			return join([]string{"-crf", quality}, lookahead("-svtav1-params", "lookahead=120"))
		}
		if rate.Lookahead {
			return []string{"-b:v", bitrate, "-svtav1-params", "rc=1:lookahead=120"}
		}
		return []string{"-b:v", bitrate, "-svtav1-params", "rc=1"}
	case "libvpx-vp9":
		if byQuality {
			return join([]string{"-crf", quality, "-b:v", "0"}, lookahead("-lag-in-frames", "25", "-row-mt", "1"))
		}
		return join([]string{"-b:v", bitrate}, lookahead("-lag-in-frames", "25", "-row-mt", "1"))
	case "h264_qsv":
		if byQuality {
			return join([]string{"-global_quality", quality}, lookahead("-look_ahead", "1", "-look_ahead_depth", "40"))
		}
		return join([]string{"-b:v", bitrate, "-maxrate", peak}, lookahead("-look_ahead", "1", "-look_ahead_depth", "40"))
	case "hevc_qsv", "av1_qsv", "vp9_qsv":
		if byQuality {
			return join([]string{"-global_quality", quality}, lookahead("-extbrc", "1", "-look_ahead_depth", "40"))
		}
		return join([]string{"-b:v", bitrate, "-maxrate", peak}, lookahead("-extbrc", "1", "-look_ahead_depth", "40"))
	case "h264_vaapi", "hevc_vaapi", "av1_vaapi", "vp9_vaapi":
		switch {
		case !byQuality:
			return []string{"-rc_mode", "VBR", "-b:v", bitrate, "-maxrate", peak}
		case rate.QualityMode == "cqp":
			return []string{"-rc_mode", "CQP", "-qp", quality}
		}
		return []string{"-rc_mode", "ICQ", "-global_quality", quality}
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc":
		if byQuality {
			return join([]string{"-rc", "vbr", "-cq", quality, "-b:v", "0"}, lookahead("-rc-lookahead", "32", "-spatial_aq", "1"))
		}
		return join([]string{"-rc", "vbr", "-b:v", bitrate, "-maxrate", peak}, lookahead("-rc-lookahead", "32"))
	}
	return nil
}
