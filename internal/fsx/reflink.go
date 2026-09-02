package fsx

import (
	"errors"
	"os"
	"syscall"
)

// ficlone is FICLONE from linux/fs.h — _IOW(0x94, 9, int). The value is the same
// on amd64 and arm64, the two architectures the image is built for.
const ficlone = 0x40049409

// Reflink clones oldPath to newPath copy-on-write, the first rung of the import ladder (ADR-11)
// (§11.2).
func (OS) Reflink(oldPath, newPath string) error {
	source, err := os.Open(oldPath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }() // a read handle; a failed close loses nothing

	destination, err := os.OpenFile(newPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return err
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, destination.Fd(), ficlone, source.Fd())
	if errno != 0 {
		// A partially created destination is worse than none: the import ladder
		// would fall through to hardlink and find the path already taken.
		_ = destination.Close()
		_ = os.Remove(newPath)
		if isUnsupportedClone(errno) {
			return ErrUnsupported
		}
		return errno
	}
	return destination.Close()
}

// isUnsupportedClone distinguishes "this filesystem cannot clone" from a real failure.
func isUnsupportedClone(errno syscall.Errno) bool {
	return errors.Is(errno, syscall.EOPNOTSUPP) ||
		errors.Is(errno, syscall.ENOTTY) ||
		errors.Is(errno, syscall.EXDEV) ||
		errors.Is(errno, syscall.EINVAL)
}
