package mediatool

import (
	"os/exec"
	"strconv"
	"sync"
)

// Nice is the CPU priority a child process runs with, through the nice tool: 19 is the lowest. The zero value leaves the process as it is.
type Nice struct{ Level int }

var (
	niceOnce sync.Once
	nicePath string
)

// Wrap puts nice in front of a command when asked for and installed; without the tool the command runs as it is.
func (n Nice) Wrap(name string, args []string) (string, []string) {
	if n.Level == 0 {
		return name, args
	}
	niceOnce.Do(func() { nicePath, _ = exec.LookPath("nice") })
	if nicePath == "" {
		return name, args
	}
	return nicePath, append([]string{"-n", strconv.Itoa(n.Level), name}, args...)
}
