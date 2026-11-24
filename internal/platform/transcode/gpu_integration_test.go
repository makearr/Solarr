package transcode_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// Runs only where a render device exists: every engine row detection calls working must convert for real, on the GPU path and on the CPU-decode path.
func TestConvertArgs_RealHardwareEveryWorkingRowEncodes(t *testing.T) {
	requireMediaTools(t)
	if _, err := os.Stat("/dev/dri"); err != nil {
		t.Skip("no /dev/dri in this environment")
	}
	service := transcode.NewService(stdoutRunner{}, nil, fsx.NewOS())
	service.Detect(t.Context())
	matrix := service.DetectEncoders(t.Context())
	source := filepath.Join(t.TempDir(), "in.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc=duration=2:size=640x360:rate=25", "-pix_fmt", "yuv420p", "-c:v", "libx264", source)
	tried := 0
	for _, row := range matrix {
		if row.Engine == transcode.Software {
			continue
		}
		t.Logf("row %s/%s encoder=%s working=%v mode=%s lookahead=%v tenbit=%v detail=%q", row.Engine, row.Codec, row.Encoder, row.Working, row.QualityMode, row.Lookahead, row.TenBit, row.Detail)
		if !row.Working {
			continue
		}
		for _, fullGPU := range []bool{true, false} {
			name := string(row.Engine) + "/" + string(row.Codec)
			if fullGPU {
				name += "/gpu-frames"
			} else {
				name += "/cpu-decode"
			}
			t.Run(name, func(t *testing.T) {
				output := filepath.Join(t.TempDir(), "out.mkv")
				convert(t, transcode.ConvertSpec{
					Input: source, Output: output, Container: "mkv", Engine: row.Engine, Device: service.DeviceFor(row.Engine), FullGPU: fullGPU,
					Video: []transcode.VideoOut{{Index: 0, Encoder: row.Encoder, Rate: transcode.RateControl{
						Mode: transcode.RateModeQuality, Quality: 28, Lookahead: row.Lookahead, QualityMode: row.QualityMode}}},
				})
				out, err := service.ProbeStreams(t.Context(), output)
				if err != nil || len(out.Streams) != 1 || out.Streams[0].Codec != string(row.Codec) {
					t.Errorf("result = %+v, %v; want one %s stream", out.Streams, err, row.Codec)
				}
			})
			tried++
		}
	}
	t.Logf("%d hardware conversions tried", tried)
}
