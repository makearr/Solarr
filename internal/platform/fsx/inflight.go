package fsx

import (
	"strings"
	"time"
)

// Names Solarr gives files while it works beside library files. A kill leaves them behind, so every reader of a
// library folder skips them and the scan sweep removes the stale ones.
const (
	MoveTempPrefix   = ".mnx-move-tmp"
	LinkTempPrefix   = ".solarr-link-tmp"
	ProbeTempPrefix  = ".solarr-probe"
	WriteProbePrefix = ".mnx-write-probe"
)

// IsInFlightName reports whether a file name is one of Solarr's own work-in-progress names.
func IsInFlightName(name string) bool {
	for _, prefix := range []string{MoveTempPrefix, LinkTempPrefix, ProbeTempPrefix, WriteProbePrefix} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// InFlightStaleAfter is how long an in-flight file may sit before it counts as an orphan: a copy of a very large
// file runs for hours, a link or a probe for milliseconds.
func InFlightStaleAfter(name string) time.Duration {
	if strings.HasPrefix(name, MoveTempPrefix) {
		return 24 * time.Hour
	}
	return time.Hour
}
