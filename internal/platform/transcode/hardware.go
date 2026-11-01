// Package transcode converts a library file into something a browser plays, with ffmpeg and,
// when one is present, a GPU encoder (documentation/media-player.md).
package transcode

import (
	"context"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Accel is how video is encoded.
type Accel string

// The encoders. Software is always available when ffmpeg is.
const (
	Software Accel = "software"
	VAAPI    Accel = "vaapi"
	QSV      Accel = "qsv"
	NVENC    Accel = "nvenc"
)

// Capability is one encoder detection tried: whether it produced a frame, and if not, why.
type Capability struct {
	Accel  Accel
	Device string
	// Name is what the device is, for the settings page ("Intel", "AMD", "NVIDIA").
	Name    string
	Working bool
	Detail  string
}

// Runner runs ffmpeg for detection; mediatool.Runner has this shape.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	Lookup(name string) error
}

const (
	renderDir  = "/dev/dri"
	sysDRMDir  = "/sys/class/drm"
	nvidiaNode = "/dev/nvidiactl"
	ffmpeg     = "ffmpeg"
)

// PCI vendor ids a render node reports.
const (
	vendorIntel  = "0x8086"
	vendorAMD    = "0x1002"
	vendorNVIDIA = "0x10de"
)

// Detect tries every encoder this machine could have and reports each outcome, best first.
// A candidate counts only if it encodes a real frame: a device node alone proves nothing
// (a missing driver, a container without the render group).
func Detect(ctx context.Context, runner Runner, fsys fsx.FS) []Capability {
	if err := runner.Lookup(ffmpeg); err != nil {
		return nil
	}
	var found []Capability

	if _, err := fsys.Stat(nvidiaNode); err == nil {
		found = append(found, try(ctx, runner, Capability{Accel: NVENC, Name: "NVIDIA"}))
	}
	for _, node := range renderNodes(fsys) {
		name := vendorName(fsys, node)
		device := path.Join(renderDir, node)
		if name == "NVIDIA" {
			continue
		}
		found = append(found,
			try(ctx, runner, Capability{Accel: VAAPI, Device: device, Name: name}),
		)
		if name == "Intel" {
			found = append(found, try(ctx, runner, Capability{Accel: QSV, Device: device, Name: name}))
		}
	}
	return found
}

// Best is the encoder auto takes: the first working one, NVENC then VAAPI then QSV.
func Best(found []Capability) (Capability, bool) {
	for _, accel := range []Accel{NVENC, VAAPI, QSV} {
		for _, capability := range found {
			if capability.Accel == accel && capability.Working {
				return capability, true
			}
		}
	}
	return Capability{}, false
}

func try(ctx context.Context, runner Runner, capability Capability) Capability {
	_, err := runner.Run(ctx, ffmpeg, probeArgs(capability)...)
	if err != nil {
		capability.Detail = lastLine(err.Error())
		return capability
	}
	capability.Working = true
	return capability
}

// probeArgs encodes one black frame with the candidate and throws it away.
func probeArgs(capability Capability) []string {
	source := []string{"-hide_banner", "-loglevel", "error"}
	var filter, encoder string
	switch capability.Accel {
	case VAAPI:
		source = append(source, "-init_hw_device", "vaapi=hw:"+capability.Device, "-filter_hw_device", "hw")
		filter, encoder = "format=nv12,hwupload", "h264_vaapi"
	case QSV:
		source = append(source, "-init_hw_device", "vaapi=va:"+capability.Device, "-init_hw_device", "qsv=hw@va", "-filter_hw_device", "hw")
		filter, encoder = "format=nv12,hwupload=extra_hw_frames=64", "h264_qsv"
	default:
		filter, encoder = "format=yuv420p", "h264_nvenc"
	}
	return append(source, "-f", "lavfi", "-i", "color=c=black:s=256x256:d=0.2", "-vf", filter,
		"-c:v", encoder, "-frames:v", "1", "-f", "null", "-")
}

func renderNodes(fsys fsx.FS) []string {
	entries, err := fsys.ReadDir(renderDir)
	if err != nil {
		return nil
	}
	var nodes []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, "renderD") {
			nodes = append(nodes, entry.Name)
		}
	}
	sort.Strings(nodes)
	return nodes
}

func vendorName(fsys fsx.FS, node string) string {
	file, err := fsys.Open(path.Join(sysDRMDir, node, "device", "vendor"))
	if err != nil {
		return "GPU"
	}
	defer func() { _ = file.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(file, 16))
	switch strings.TrimSpace(string(raw)) {
	case vendorIntel:
		return "Intel"
	case vendorAMD:
		return "AMD"
	case vendorNVIDIA:
		return "NVIDIA"
	}
	return "GPU"
}

// lastLine is the tail of ffmpeg's complaint, which is where the cause is.
func lastLine(message string) string {
	lines := strings.Split(strings.TrimSpace(message), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
