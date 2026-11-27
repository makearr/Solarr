package transcode

import (
	"io"
	"math"
	"runtime"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// The files a container's CPU limit is read from: cgroup v2, cgroup v1, and the cores the process may be scheduled on.
const (
	cgroupV2Max     = "/sys/fs/cgroup/cpu.max"
	cgroupV1Quota   = "/sys/fs/cgroup/cpu/cpu.cfs_quota_us"
	cgroupV1Period  = "/sys/fs/cgroup/cpu/cpu.cfs_period_us"
	procStatusFile  = "/proc/self/status"
	allowedCoresKey = "Cpus_allowed_list:"
)

// CPUCores is how many cores this process may use: the container's CPU quota when it has one, else the cores the
// scheduler allows it, else every core the machine has.
func CPUCores(fsys fsx.FS) int {
	if quota := cgroupQuotaCores(fsys); quota > 0 {
		return quota
	}
	if allowed := allowedCores(fsys); allowed > 0 {
		return allowed
	}
	return runtime.NumCPU()
}

// cgroupQuotaCores is the quota rounded up to whole cores; zero when there is none ("max") or no cgroup file.
func cgroupQuotaCores(fsys fsx.FS) int {
	if fields := strings.Fields(readSmall(fsys, cgroupV2Max)); len(fields) == 2 && fields[0] != "max" {
		return wholeCores(fields[0], fields[1])
	}
	return wholeCores(strings.TrimSpace(readSmall(fsys, cgroupV1Quota)), strings.TrimSpace(readSmall(fsys, cgroupV1Period)))
}

func wholeCores(quota, period string) int {
	q, quotaErr := strconv.ParseFloat(quota, 64)
	p, periodErr := strconv.ParseFloat(period, 64)
	if quotaErr != nil || periodErr != nil || q <= 0 || p <= 0 {
		return 0
	}
	return int(math.Ceil(q / p))
}

// allowedCores counts the cores in the "Cpus_allowed_list: 0-3,6" line of the process status; zero when it cannot be read.
func allowedCores(fsys fsx.FS) int {
	for _, line := range strings.Split(readSmall(fsys, procStatusFile), "\n") {
		list, found := strings.CutPrefix(line, allowedCoresKey)
		if !found {
			continue
		}
		total := 0
		for _, part := range strings.Split(strings.TrimSpace(list), ",") {
			low, high, isRange := strings.Cut(part, "-")
			first, err := strconv.Atoi(strings.TrimSpace(low))
			if err != nil {
				return 0
			}
			last := first
			if isRange {
				if last, err = strconv.Atoi(strings.TrimSpace(high)); err != nil || last < first {
					return 0
				}
			}
			total += last - first + 1
		}
		return total
	}
	return 0
}

func readSmall(fsys fsx.FS, name string) string {
	file, err := fsys.Open(name)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(file, 8192))
	return string(raw)
}
