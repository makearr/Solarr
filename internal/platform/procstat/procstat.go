// Package procstat reads Solarr's own CPU, memory and IO from /proc/self; it never looks at the rest of the host.
package procstat

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

const (
	// ticksPerSecond is USER_HZ, which is 100 on every Linux Solarr runs on.
	ticksPerSecond = 100
	pageBytes      = 4096
)

// Reading is the process's state at one moment: totals since start, except Resident.
type Reading struct {
	CPUSeconds    float64
	ResidentBytes int64
	// ReadBytes and WriteBytes count read(2) and write(2) traffic, files and sockets alike.
	ReadBytes, WriteBytes int64
	// DiskReadBytes and DiskWriteBytes count what reached the storage layer.
	DiskReadBytes, DiskWriteBytes int64
	At                            time.Time
}

// Rates is what the process did between two readings.
type Rates struct {
	CPUPercent                                float64 // of one core
	ResidentBytes                             int64
	ReadBytesPerSec, WriteBytesPerSec         float64
	DiskReadBytesPerSec, DiskWriteBytesPerSec float64
}

// Sampler reads /proc/self through the filesystem seam.
type Sampler struct {
	fsys  fsx.FS
	clock clock.Clock
	last  *Reading
}

// New builds a sampler.
func New(fsys fsx.FS, c clock.Clock) *Sampler { return &Sampler{fsys: fsys, clock: c} }

func (s *Sampler) readAll(path string) (string, bool) {
	file, err := s.fsys.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close() //nolint:errcheck // read-only
	data, err := io.ReadAll(bufio.NewReader(file))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// Read takes one reading; the second result is false where /proc is not readable.
func (s *Sampler) Read() (Reading, bool) {
	reading := Reading{At: s.clock.Now()}
	stat, ok := s.readAll("/proc/self/stat")
	if !ok {
		return reading, false
	}
	// The command name is parenthesised and may hold spaces; the fields that matter follow its closing bracket.
	if end := strings.LastIndexByte(stat, ')'); end >= 0 {
		fields := strings.Fields(stat[end+1:])
		// fields[0] is field 3 (state); utime and stime are fields 14 and 15.
		if len(fields) > 12 {
			user, _ := strconv.ParseInt(fields[11], 10, 64)
			system, _ := strconv.ParseInt(fields[12], 10, 64)
			reading.CPUSeconds = float64(user+system) / ticksPerSecond
		}
	}
	if statm, ok := s.readAll("/proc/self/statm"); ok {
		if fields := strings.Fields(statm); len(fields) > 1 {
			pages, _ := strconv.ParseInt(fields[1], 10, 64)
			reading.ResidentBytes = pages * pageBytes
		}
	}
	if counters, ok := s.readAll("/proc/self/io"); ok {
		for _, line := range strings.Split(counters, "\n") {
			name, value, found := strings.Cut(line, ": ")
			if !found {
				continue
			}
			number, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			switch name {
			case "rchar":
				reading.ReadBytes = number
			case "wchar":
				reading.WriteBytes = number
			case "read_bytes":
				reading.DiskReadBytes = number
			case "write_bytes":
				reading.DiskWriteBytes = number
			}
		}
	}
	return reading, true
}

// Rates takes a reading and answers what happened since the previous one; false on the first call or without /proc.
func (s *Sampler) Rates() (Rates, bool) {
	now, ok := s.Read()
	if !ok {
		return Rates{}, false
	}
	previous := s.last
	s.last = &now
	if previous == nil {
		return Rates{}, false
	}
	seconds := now.At.Sub(previous.At).Seconds()
	if seconds <= 0 {
		return Rates{}, false
	}
	return Rates{
		CPUPercent:           100 * (now.CPUSeconds - previous.CPUSeconds) / seconds,
		ResidentBytes:        now.ResidentBytes,
		ReadBytesPerSec:      float64(now.ReadBytes-previous.ReadBytes) / seconds,
		WriteBytesPerSec:     float64(now.WriteBytes-previous.WriteBytes) / seconds,
		DiskReadBytesPerSec:  float64(now.DiskReadBytes-previous.DiskReadBytes) / seconds,
		DiskWriteBytesPerSec: float64(now.DiskWriteBytes-previous.DiskWriteBytes) / seconds,
	}, true
}
