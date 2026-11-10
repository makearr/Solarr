package mediatool

import (
	"os/exec"
	"strconv"
	"sync"
)

// IONice is the disk priority a child process runs with, through the ionice tool: Class 2 is best effort with Level 0
// (first) to 7 (last), Class 3 is idle. The zero value leaves the process as it is.
type IONice struct{ Class, Level int }

var (
	ioniceOnce sync.Once
	ionicePath string
)

// Wrap puts ionice in front of a command when asked for and installed; without the tool the command runs as it is.
func (n IONice) Wrap(name string, args []string) (string, []string) {
	if n.Class == 0 {
		return name, args
	}
	ioniceOnce.Do(func() { ionicePath, _ = exec.LookPath("ionice") })
	if ionicePath == "" {
		return name, args
	}
	wrapped := []string{"-c", strconv.Itoa(n.Class)}
	if n.Class != 3 {
		wrapped = append(wrapped, "-n", strconv.Itoa(n.Level))
	}
	return ionicePath, append(append(wrapped, name), args...)
}
