package ramcopy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Memory is what the container may still use. LimitBytes is zero when the container has no memory limit, in which
// case the host's figure is used and Source says so.
type Memory struct {
	LimitBytes, UsedBytes, AvailableBytes int64
	Source                                string
}

const (
	cgroupV2Max     = "/sys/fs/cgroup/memory.max"
	cgroupV2Current = "/sys/fs/cgroup/memory.current"
	cgroupV2Stat    = "/sys/fs/cgroup/memory.stat"
	cgroupV1Limit   = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	cgroupV1Usage   = "/sys/fs/cgroup/memory/memory.usage_in_bytes"
	cgroupV1Stat    = "/sys/fs/cgroup/memory/memory.stat"
	procMeminfo     = "/proc/meminfo"
	// unlimitedFloor: a cgroup v1 limit above this is "no limit" (the kernel stores a huge page-aligned number).
	unlimitedFloor = int64(1) << 60
)

// ReadMemory answers the container's memory headroom from its cgroup, the host's only when the cgroup sets no limit.
// Reclaimable file cache is not counted as used, as docker stats does not.
func ReadMemory(fsys fsx.FS) (Memory, error) {
	if limit, ok := readNumber(fsys, cgroupV2Max); ok {
		used, _ := readNumber(fsys, cgroupV2Current)
		return limited(limit, used-inactiveFile(fsys, cgroupV2Stat), "cgroup v2"), nil
	}
	if text := readText(fsys, cgroupV2Max); strings.TrimSpace(text) == "max" {
		return hostMemory(fsys)
	}
	if limit, ok := readNumber(fsys, cgroupV1Limit); ok && limit < unlimitedFloor {
		used, _ := readNumber(fsys, cgroupV1Usage)
		return limited(limit, used-inactiveFile(fsys, cgroupV1Stat), "cgroup v1"), nil
	}
	return hostMemory(fsys)
}

func limited(limit, used int64, source string) Memory {
	used = max(used, 0)
	return Memory{LimitBytes: limit, UsedBytes: used, AvailableBytes: max(limit-used, 0), Source: source}
}

func hostMemory(fsys fsx.FS) (Memory, error) {
	file, err := fsys.Open(procMeminfo)
	if err != nil {
		return Memory{}, fmt.Errorf("ramcopy: read memory: %w", err)
	}
	defer func() { _ = file.Close() }()
	return parseMeminfo(file)
}

func parseMeminfo(r io.Reader) (Memory, error) {
	var total, available int64
	var haveAvailable bool
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		name, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kilobytes, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "MemTotal":
			total = kilobytes * 1024
		case "MemAvailable":
			available, haveAvailable = kilobytes*1024, true
		}
	}
	if total == 0 || !haveAvailable {
		return Memory{}, errors.New("ramcopy: /proc/meminfo has no MemTotal and MemAvailable")
	}
	return Memory{LimitBytes: 0, UsedBytes: total - available, AvailableBytes: available, Source: "host (the container has no memory limit)"}, nil
}

// inactiveFile is the reclaimable page cache a cgroup's usage includes.
func inactiveFile(fsys fsx.FS, statPath string) int64 {
	scanner := bufio.NewScanner(strings.NewReader(readText(fsys, statPath)))
	for scanner.Scan() {
		if name, value, ok := strings.Cut(scanner.Text(), " "); ok && (name == "inactive_file" || name == "total_inactive_file") {
			if bytes, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				return bytes
			}
		}
	}
	return 0
}

func readText(fsys fsx.FS, path string) string {
	file, err := fsys.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20))
	if err != nil {
		return ""
	}
	return string(data)
}

func readNumber(fsys fsx.FS, path string) (int64, bool) {
	value, err := strconv.ParseInt(strings.TrimSpace(readText(fsys, path)), 10, 64)
	return value, err == nil
}
