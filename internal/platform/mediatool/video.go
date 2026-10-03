package mediatool

import (
	"strconv"
	"strings"
)

// frameRate reads ffprobe's "24000/1001"; the average first, the nominal rate when that is zero.
func frameRate(average, nominal string) float64 {
	for _, text := range []string{average, nominal} {
		numerator, denominator, found := strings.Cut(text, "/")
		if !found {
			continue
		}
		top, errTop := strconv.ParseFloat(numerator, 64)
		bottom, errBottom := strconv.ParseFloat(denominator, 64)
		if errTop == nil && errBottom == nil && bottom > 0 && top > 0 {
			return top / bottom
		}
	}
	return 0
}

// scanType is "interlaced" for any field order but progressive; empty when ffprobe says nothing.
func scanType(fieldOrder string) string {
	switch fieldOrder {
	case "":
		return ""
	case "progressive", "unknown":
		return "progressive"
	}
	return "interlaced"
}

// hdrOf names the HDR format a stream carries in the vocabulary a release name uses; "" is sdr.
func hdrOf(colorTransfer string, hasSideData func(kind string) bool) string {
	switch {
	case hasSideData("dovi"):
		return "dolby-vision"
	case hasSideData("smpte2094-40"):
		return "hdr10plus"
	case colorTransfer == "smpte2084":
		return "hdr10"
	case colorTransfer == "arib-std-b67":
		return "hlg"
	}
	return ""
}

// channelLayout is "5.1" for ffprobe's "5.1(side)"; without a layout the channel count stands for the usual ones.
func channelLayout(layout string, channels int) string {
	if base, _, _ := strings.Cut(layout, "("); base != "" && strings.ContainsAny(base, ".") {
		return base
	}
	switch channels {
	case 1:
		return "1.0"
	case 2:
		return "2.0"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	if channels > 0 {
		return strconv.Itoa(channels)
	}
	return ""
}

func uniqueStrings(values []string) []string {
	var out []string
	for _, value := range values {
		found := false
		for _, have := range out {
			if have == value {
				found = true
			}
		}
		if !found {
			out = append(out, value)
		}
	}
	return out
}
