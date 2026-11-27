package transcode

import (
	"context"
	"path"
	"regexp"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// EngineCounts is how many hardware encoder and decoder engines a device has; zero is not known.
type EngineCounts struct {
	Encode, Decode int
}

// nvidiaSmi names the installed GPUs; the container needs the NVIDIA runtime for it to exist.
const nvidiaSmi = "nvidia-smi"

// nvidiaEngineTable holds the cards whose engine counts NVIDIA's encode/decode matrix states; any other card is unknown rather than guessed.
var nvidiaEngineTable = []struct {
	pattern *regexp.Regexp
	engines EngineCounts
}{
	{regexp.MustCompile(`(?i)\bRTX 40[89]0\b`), EngineCounts{Encode: 2, Decode: 2}},
	{regexp.MustCompile(`(?i)\b(GTX 16[0-9]{2}|RTX 20[0-9]{2})\b`), EngineCounts{Encode: 1, Decode: 1}},
}

// EnginesOfModel is the engine counts of an NVIDIA card by name; zero for a card the table does not hold.
func EnginesOfModel(model string) EngineCounts {
	for _, row := range nvidiaEngineTable {
		if row.pattern.MatchString(model) {
			return row.engines
		}
	}
	return EngineCounts{}
}

// nvidiaModel is the card's name as nvidia-smi gives it; empty when the tool is missing or fails.
func nvidiaModel(ctx context.Context, runner Runner) string {
	if runner.Lookup(nvidiaSmi) != nil {
		return ""
	}
	output, err := runner.Run(ctx, nvidiaSmi, "--query-gpu=name", "--format=csv,noheader")
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	return strings.TrimSpace(line)
}

// sysfsEngines counts the video engines the kernel lists for a render node: i915 names each one under the card's
// engine directory (vcs0, vcs1: video decode and encode), amdgpu lists its VCN instances in the IP discovery tree. A
// kernel that lists none (or a sysfs this process cannot read) is unknown, never guessed.
func sysfsEngines(fsys fsx.FS, node string) EngineCounts {
	device := path.Join(sysDRMDir, node, "device")
	count := 0
	if cards, err := fsys.ReadDir(path.Join(device, "drm")); err == nil {
		for _, card := range cards {
			if !strings.HasPrefix(card.Name, "card") || strings.Contains(card.Name, "-") {
				continue
			}
			engines, err := fsys.ReadDir(path.Join(device, "drm", card.Name, "engine"))
			if err != nil {
				continue
			}
			for _, engine := range engines {
				if strings.HasPrefix(engine.Name, "vcs") {
					count++
				}
			}
		}
	}
	if count == 0 {
		count = vcnInstances(fsys, path.Join(device, "ip_discovery", "die"))
	}
	return EngineCounts{Encode: count, Decode: count}
}

// vcnInstances counts the video core instances amdgpu lists under each die.
func vcnInstances(fsys fsx.FS, dies string) int {
	entries, err := fsys.ReadDir(dies)
	if err != nil {
		return 0
	}
	count := 0
	for _, die := range entries {
		instances, err := fsys.ReadDir(path.Join(dies, die.Name, "VCN"))
		if err != nil {
			continue
		}
		for _, instance := range instances {
			if instance.IsDir {
				count++
			}
		}
	}
	return count
}
