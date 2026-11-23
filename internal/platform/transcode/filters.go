package transcode

import (
	"slices"
	"strconv"
	"strings"
)

// VideoFilterKind names one video filter a conversion can apply.
type VideoFilterKind string

// The video filters, in the order the chain applies them.
const (
	FilterDeinterlace VideoFilterKind = "deinterlace"
	FilterDeflicker   VideoFilterKind = "deflicker"
	FilterHQDN3D      VideoFilterKind = "hqdn3d"
	FilterNLMeans     VideoFilterKind = "nlmeans"
	FilterBM3D        VideoFilterKind = "bm3d"
	FilterDeblock     VideoFilterKind = "deblock"
	FilterDeband      VideoFilterKind = "deband"
	FilterCAS         VideoFilterKind = "cas"
	FilterSmartBlur   VideoFilterKind = "smartblur"
)

// Strengths a filter works at.
const (
	StrengthLow    = "low"
	StrengthMedium = "medium"
	StrengthStrong = "strong"
)

// HDR handling of an encoded video stream.
const (
	HDRKeep    = "keep"
	HDRTonemap = "tonemap"
)

// VideoFilter is one filter at one strength; deinterlace has no strength.
type VideoFilter struct {
	Kind     VideoFilterKind
	Strength string
}

// strengthTable is a filter's expression at low, medium and strong.
type strengthTable [3]string

var softwareFilters = map[VideoFilterKind]strengthTable{
	FilterDeflicker: {"deflicker=size=5", "deflicker=size=7", "deflicker=size=10"},
	FilterHQDN3D:    {"hqdn3d=2:1.5:3:2.25", "hqdn3d=4:3:6:4.5", "hqdn3d=8:6:12:9"},
	FilterNLMeans:   {"nlmeans=s=1.5:p=7:r=9", "nlmeans=s=3:p=7:r=11", "nlmeans=s=5:p=7:r=15"},
	FilterBM3D:      {"bm3d=sigma=3:block=8:bstep=4:group=1", "bm3d=sigma=6:block=8:bstep=4:group=1", "bm3d=sigma=10:block=8:bstep=4:group=1"},
	FilterDeblock:   {"deblock=filter=weak:block=4", "deblock=filter=strong:block=4", "deblock=filter=strong:block=4:alpha=0.15:beta=0.1"},
	FilterDeband:    {"deband=1thr=0.01:2thr=0.01:3thr=0.01", "deband=1thr=0.02:2thr=0.02:3thr=0.02", "deband=1thr=0.04:2thr=0.04:3thr=0.04"},
	FilterCAS:       {"cas=strength=0.3", "cas=strength=0.5", "cas=strength=0.8"},
	FilterSmartBlur: {"smartblur=lr=1:ls=-0.3", "smartblur=lr=1:ls=-0.6", "smartblur=lr=1:ls=-1.0"},
}

const tonemapChain = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv"

func strengthIndex(strength string) int {
	switch strength {
	case StrengthLow:
		return 0
	case StrengthStrong:
		return 2
	}
	return 1
}

func (v VideoOut) filter(kinds ...VideoFilterKind) (VideoFilter, bool) {
	for _, filter := range v.Filters {
		if slices.Contains(kinds, filter.Kind) {
			return filter, true
		}
	}
	return VideoFilter{}, false
}

func formatFPS(fps float64) string { return strconv.FormatFloat(fps, 'f', -1, 64) }

func (v VideoOut) pixelFormat() string {
	if v.TenBit {
		return "yuv420p10le"
	}
	return "yuv420p"
}

func (v VideoOut) uploadFormat() string {
	if v.TenBit {
		return "p010"
	}
	return "nv12"
}

// softwareChain is the filter chain run on the CPU, in the fixed order, ending in the pixel format.
func softwareChain(v VideoOut) string {
	return strings.Join(append(softwareParts(v), "format="+v.pixelFormat()), ",")
}

func softwareParts(v VideoOut) []string {
	var parts []string
	if _, on := v.filter(FilterDeinterlace); on {
		parts = append(parts, "bwdif=mode=send_frame")
	}
	for _, kinds := range [][]VideoFilterKind{{FilterDeflicker}, {FilterHQDN3D, FilterNLMeans, FilterBM3D}, {FilterDeblock}, {FilterDeband}} {
		if filter, on := v.filter(kinds...); on {
			parts = append(parts, softwareFilters[filter.Kind][strengthIndex(filter.Strength)])
		}
	}
	if v.HDR == HDRTonemap {
		parts = append(parts, tonemapChain)
	}
	if v.Width > 0 || v.Height > 0 {
		width, height := "-2", "-2"
		if v.Width > 0 {
			width = strconv.Itoa(v.Width)
		} else {
			height = strconv.Itoa(v.Height)
		}
		parts = append(parts, "scale="+width+":"+height+":flags=lanczos")
	}
	if v.FPS > 0 {
		parts = append(parts, "fps="+formatFPS(v.FPS))
	}
	if filter, on := v.filter(FilterCAS, FilterSmartBlur); on {
		parts = append(parts, softwareFilters[filter.Kind][strengthIndex(filter.Strength)])
	}
	return parts
}

// gpuForms are the filters the GPU engines have a form for.
var gpuForms = map[Accel][]VideoFilterKind{
	QSV:   {FilterDeinterlace, FilterHQDN3D, FilterNLMeans, FilterBM3D},
	VAAPI: {FilterDeinterlace, FilterHQDN3D, FilterNLMeans, FilterBM3D},
	NVENC: {FilterDeinterlace, FilterHQDN3D, FilterNLMeans, FilterBM3D},
}

var (
	qsvDenoise   = [3]string{"20", "40", "70"}
	vaapiDenoise = [3]string{"16", "32", "48"}
	cudaSigma    = [3]string{"0.1", "0.2", "0.4"}
)

// gpuChain is the filter chain run on the GPU; false when an active filter has no form for the engine.
func gpuChain(engine Accel, v VideoOut) (string, bool) {
	for _, filter := range v.Filters {
		if !slices.Contains(gpuForms[engine], filter.Kind) {
			return "", false
		}
	}
	if v.HDR == HDRTonemap && engine == NVENC {
		return "", false
	}
	scaling := v.Width > 0 && v.Height > 0
	if (v.Width > 0 || v.Height > 0) && !scaling {
		return "", false
	}
	size := strconv.Itoa(v.Width) + ":" + strconv.Itoa(v.Height)
	_, deinterlace := v.filter(FilterDeinterlace)
	denoise, denoising := v.filter(FilterHQDN3D, FilterNLMeans, FilterBM3D)
	index := strengthIndex(denoise.Strength)
	var parts []string
	switch engine {
	case QSV:
		var options []string
		if deinterlace {
			options = append(options, "deinterlace=2")
		}
		if denoising {
			options = append(options, "denoise="+qsvDenoise[index])
		}
		if scaling {
			options = append(options, "w="+strconv.Itoa(v.Width), "h="+strconv.Itoa(v.Height))
		}
		if v.FPS > 0 {
			options = append(options, "framerate="+formatFPS(v.FPS))
		}
		if v.HDR == HDRTonemap {
			options = append(options, "tonemap=1")
		}
		if len(options) > 0 || v.TenBit {
			parts = append(parts, "vpp_qsv="+strings.Join(append(options, "format="+v.uploadFormat()), ":"))
		}
	case VAAPI:
		if deinterlace {
			parts = append(parts, "deinterlace_vaapi")
		}
		if denoising {
			parts = append(parts, "denoise_vaapi=denoise="+vaapiDenoise[index])
		}
		if v.HDR == HDRTonemap {
			parts = append(parts, "tonemap_vaapi=format=nv12:t=bt709:m=bt709:p=bt709")
		}
		switch {
		case scaling:
			parts = append(parts, "scale_vaapi=w="+strconv.Itoa(v.Width)+":h="+strconv.Itoa(v.Height)+":format="+v.uploadFormat())
		case v.TenBit && v.HDR != HDRTonemap:
			parts = append(parts, "scale_vaapi=format=p010")
		}
		if v.FPS > 0 {
			parts = append(parts, "fps="+formatFPS(v.FPS))
		}
	case NVENC:
		if deinterlace {
			parts = append(parts, "bwdif_cuda")
		}
		if denoising {
			parts = append(parts, "bilateral_cuda=sigmaS=1:sigmaR="+cudaSigma[index])
		}
		if scaling {
			parts = append(parts, "scale_cuda="+size)
		}
		if v.FPS > 0 {
			parts = append(parts, "fps="+formatFPS(v.FPS))
		}
	default:
		return "", false
	}
	return strings.Join(parts, ","), true
}

// CanRunOnGPU says whether every filter the stream needs has a form on the engine, so frames can stay on the GPU.
func CanRunOnGPU(engine Accel, v VideoOut) bool {
	_, ok := gpuChain(engine, v)
	return ok
}

// uploadChain is the software chain followed by the upload the GPU encoder reads; NVENC takes system frames.
func uploadChain(engine Accel, v VideoOut) string {
	chain := strings.Join(append(softwareParts(v), "format="+v.uploadFormat()), ",")
	switch engine {
	case VAAPI:
		return chain + ",hwupload"
	case QSV:
		return chain + ",hwupload=extra_hw_frames=64"
	}
	return chain
}
