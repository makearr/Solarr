package transcode

import (
	"context"
	"regexp"
	"strings"
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
