// Package transcode converts a library file into something a browser plays, with ffmpeg and,
// when one is present, a GPU encoder (documentation/media-player.md).
package transcode

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"slices"
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
	methods := hwaccelMethods(ctx, runner)

	if _, err := fsys.Stat(nvidiaNode); err == nil {
		found = append(found, try(ctx, runner, lacking(methods, "cuda", Capability{Accel: NVENC, Name: "NVIDIA"})))
	}
	nodes := renderNodes(fsys)
	if len(nodes) == 0 {
		found = append(found, Capability{Accel: VAAPI, Name: "GPU", Detail: "no render device: start the container with --device /dev/dri (compose: devices: /dev/dri:/dev/dri)"})
	}
	for _, node := range nodes {
		name := vendorName(fsys, node)
		device := path.Join(renderDir, node)
		if name == "NVIDIA" {
			continue
		}
		unreadable := openError(fsys, device)
		found = append(found,
			try(ctx, runner, withAccess(unreadable, lacking(methods, "vaapi", Capability{Accel: VAAPI, Device: device, Name: name}))),
		)
		if name == "Intel" || name == "GPU" {
			found = append(found, try(ctx, runner, withAccess(unreadable, lacking(methods, "qsv", Capability{Accel: QSV, Device: device, Name: name}))))
		}
	}
	return found
}

// hwaccelMethods is what this ffmpeg was built with; nil when it cannot say.
func hwaccelMethods(ctx context.Context, runner Runner) []string {
	output, err := runner.Run(ctx, ffmpeg, "-hide_banner", "-hwaccels")
	if err != nil {
		return nil
	}
	var methods []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasSuffix(line, ":") {
			methods = append(methods, line)
		}
	}
	return methods
}

// lacking marks a candidate failed up front when ffmpeg names its methods and the one it needs is not among them.
func lacking(methods []string, method string, capability Capability) Capability {
	if len(methods) > 0 && !slices.Contains(methods, method) {
		capability.Detail = "this ffmpeg was built without " + method
	}
	return capability
}

// openError is why a render node cannot be opened by this process, empty when it can.
func openError(fsys fsx.FS, device string) string {
	file, err := fsys.Open(device)
	if err == nil {
		_ = file.Close()
		return ""
	}
	if errors.Is(err, fs.ErrPermission) {
		return "no permission on " + device + ": add the container to the host's render group (group_add: the id from getent group render)"
	}
	return err.Error()
}

func withAccess(problem string, capability Capability) Capability {
	if problem != "" && capability.Detail == "" {
		capability.Detail = problem
	}
	return capability
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
	if capability.Detail != "" {
		return capability
	}
	_, err := runner.Run(ctx, ffmpeg, probeArgs(capability)...)
	if err != nil {
		capability.Detail = hint(lastLine(err.Error()))
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

// hint appends the usual fix to the two failures a container produces most.
func hint(line string) string {
	switch {
	case strings.Contains(line, "Permission denied"):
		return line + " (add the container to the host's render group)"
	case strings.Contains(line, "No such file"):
		return line + " (pass --device /dev/dri into the container)"
	}
	return line
}

// DecodeArgs are the input options that put frame decoding on the best working GPU; nil means decode on the processor.
func DecodeArgs(capability Capability, ok bool) []string {
	if !ok {
		return nil
	}
	switch capability.Accel {
	case VAAPI, QSV:
		return []string{"-hwaccel", "vaapi", "-hwaccel_device", capability.Device}
	case NVENC:
		return []string{"-hwaccel", "cuda"}
	}
	return nil
}
