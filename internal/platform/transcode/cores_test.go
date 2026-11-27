package transcode

import (
	"runtime"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func memFiles(t *testing.T, files map[string]string) *fsx.Mem {
	t.Helper()
	memory := fsx.NewMem(clock.NewFakeAtEpoch())
	for name, text := range files {
		if err := memory.MkdirAll(name[:len(name)-len(baseName(name))-1], 0o755); err != nil {
			t.Fatal(err)
		}
		writer, err := memory.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return memory
}

func baseName(name string) string {
	for index := len(name) - 1; index >= 0; index-- {
		if name[index] == '/' {
			return name[index+1:]
		}
	}
	return name
}

func TestCPUCores_ReadsTheQuotaThenTheAllowedCoresThenTheMachine(t *testing.T) {
	for _, check := range []struct {
		name  string
		files map[string]string
		want  int
	}{
		{"cgroup v2 quota rounds up", map[string]string{cgroupV2Max: "250000 100000\n"}, 3},
		{"cgroup v2 unlimited falls to the allowed cores", map[string]string{cgroupV2Max: "max 100000\n", procStatusFile: "Name:\tsolarr\nCpus_allowed_list:\t0-3,6\n"}, 5},
		{"cgroup v1 quota", map[string]string{cgroupV1Quota: "200000\n", cgroupV1Period: "100000\n"}, 2},
		{"cgroup v1 unlimited", map[string]string{cgroupV1Quota: "-1\n", cgroupV1Period: "100000\n", procStatusFile: "Cpus_allowed_list:\t0-1\n"}, 2},
		{"nothing readable is the machine's cores", map[string]string{}, runtime.NumCPU()},
		{"an unreadable allowed list is the machine's cores", map[string]string{procStatusFile: "Cpus_allowed_list:\tx\n"}, runtime.NumCPU()},
	} {
		if got := CPUCores(memFiles(t, check.files)); got != check.want {
			t.Errorf("%s: cores = %d, want %d", check.name, got, check.want)
		}
	}
}
