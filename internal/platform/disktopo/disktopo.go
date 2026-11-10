// Package disktopo works out which paths share a filesystem, a pool or a physical disk, from the container's own
// view (/proc/self/mountinfo and /sys), so IO can be scheduled per disk rather than per path.
package disktopo

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

const (
	mountinfoPath = "/proc/self/mountinfo"
	sysBlockDir   = "/sys/dev/block"
)

// Mount is one line of mountinfo.
type Mount struct {
	DeviceID   string // "major:minor"
	MountPoint string
	FSType     string
	Source     string
}

// Layout is where one path lives, from the most specific unit to the most shared one.
type Layout struct {
	Path       string
	MountPoint string
	FSType     string
	Source     string
	Filesystem string // the mount's device id; two paths with the same value share a filesystem
	Pool       string // "zfs:tank" or "nfs:server"; empty when the filesystem is not pooled
	Disk       string // physical disk name, e.g. "sda"; empty when /sys does not say
	Evidence   string // why the group was chosen, for the admin who has to correct it
	TotalBytes int64
	FreeBytes  int64
}

// Group is the coarsest unit the path is known to share with others: what IO contends on.
func (l Layout) Group() string {
	switch {
	case l.Pool != "":
		return l.Pool
	case l.Disk != "":
		return "disk:" + l.Disk
	default:
		return "fs:" + l.Filesystem
	}
}

// ParseMountinfo reads the mountinfo format, which has a variable number of optional fields before the "-" separator.
func ParseMountinfo(r io.Reader) ([]Mount, error) {
	var mounts []Mount
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		separator := indexOf(fields, "-")
		if separator < 6 || len(fields) < separator+3 {
			continue
		}
		mounts = append(mounts, Mount{
			DeviceID: fields[2], MountPoint: unescape(fields[4]),
			FSType: fields[separator+1], Source: unescape(fields[separator+2]),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("disktopo: read mountinfo: %w", err)
	}
	return mounts, nil
}

func indexOf(fields []string, want string) int {
	for i, field := range fields {
		if field == want {
			return i
		}
	}
	return -1
}

// unescape undoes mountinfo's octal escapes (a space is \040).
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if value, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(value))
				i += 3
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// Detect reads the layout of every path. A path that cannot be read gets what is known and an Evidence saying why.
func Detect(fsys fsx.FS, paths []string) []Layout {
	mounts := readMounts(fsys)
	layouts := make([]Layout, 0, len(paths))
	for _, path := range paths {
		layouts = append(layouts, detectOne(fsys, mounts, path))
	}
	groupBySpace(layouts)
	return layouts
}

func readMounts(fsys fsx.FS) []Mount {
	file, err := fsys.Open(mountinfoPath)
	if err != nil {
		return nil
	}
	defer file.Close() //nolint:errcheck // read-only
	mounts, _ := ParseMountinfo(file)
	return mounts
}

func detectOne(fsys fsx.FS, mounts []Mount, path string) Layout {
	layout := Layout{Path: path}
	if total, free, err := fsys.DiskUsage(path); err == nil {
		layout.TotalBytes, layout.FreeBytes = total, free
	}
	resolved, err := fsys.Resolve(path)
	if err != nil {
		resolved = filepath.Clean(path)
	}
	mount, found := owningMount(mounts, resolved)
	if !found {
		if id, idErr := fsys.FilesystemID(path); idErr == nil {
			layout.Filesystem = id
		}
		layout.Evidence = "mount table unavailable: the filesystem number is all that is known"
		return layout
	}
	layout.MountPoint, layout.FSType, layout.Source, layout.Filesystem = mount.MountPoint, mount.FSType, mount.Source, mount.DeviceID
	switch {
	case mount.FSType == "zfs":
		layout.Pool = "zfs:" + strings.SplitN(mount.Source, "/", 2)[0]
		layout.Evidence = "ZFS dataset " + mount.Source + " lives in pool " + strings.TrimPrefix(layout.Pool, "zfs:")
	case strings.HasPrefix(mount.FSType, "nfs") || mount.FSType == "cifs" || mount.FSType == "smb3":
		layout.Pool = "net:" + serverOf(mount.Source)
		layout.Evidence = "network share: every export of " + serverOf(mount.Source) + " shares one server"
	default:
		layout.Disk = physicalDisk(fsys, mount.DeviceID)
		if layout.Disk != "" {
			layout.Evidence = "block device " + mount.Source + " is on disk " + layout.Disk
		} else {
			layout.Evidence = "no block device behind " + mount.Source + " (" + mount.FSType + ")"
		}
	}
	return layout
}

// owningMount is the mount with the longest mount point that contains the path.
func owningMount(mounts []Mount, path string) (Mount, bool) {
	var best Mount
	found := false
	for _, mount := range mounts {
		if !within(mount.MountPoint, path) {
			continue
		}
		if !found || len(mount.MountPoint) >= len(best.MountPoint) {
			best, found = mount, true
		}
	}
	return best, found
}

func within(mountPoint, path string) bool {
	if mountPoint == "/" {
		return true
	}
	return path == mountPoint || strings.HasPrefix(path, mountPoint+"/")
}

// serverOf takes "host:/export" or "//host/share".
func serverOf(source string) string {
	trimmed := strings.TrimPrefix(source, "//")
	if i := strings.IndexAny(trimmed, ":/"); i > 0 {
		return trimmed[:i]
	}
	return trimmed
}

// physicalDisk follows /sys from a partition to its disk and from a device-mapper volume to its first member.
func physicalDisk(fsys fsx.FS, deviceID string) string {
	if strings.HasPrefix(deviceID, "0:") {
		return ""
	}
	resolved, err := fsys.Resolve(sysBlockDir + "/" + deviceID)
	if err != nil {
		return ""
	}
	return diskOf(fsys, resolved, 0)
}

func diskOf(fsys fsx.FS, device string, depth int) string {
	if depth > 4 {
		return filepath.Base(device)
	}
	if _, err := fsys.Stat(device + "/partition"); err == nil {
		return filepath.Base(filepath.Dir(device))
	}
	if members, err := fsys.ReadDir(device + "/slaves"); err == nil && len(members) > 0 {
		if member, err := fsys.Resolve(device + "/slaves/" + members[0].Name); err == nil {
			return diskOf(fsys, member, depth+1)
		}
	}
	return filepath.Base(device)
}

// groupBySpace joins filesystems nothing else could place when they report the same size and free space, which two
// views of one volume do and two disks almost never do.
func groupBySpace(layouts []Layout) {
	for i := range layouts {
		if layouts[i].Pool != "" || layouts[i].Disk != "" || layouts[i].TotalBytes == 0 {
			continue
		}
		for j := range layouts[:i] {
			other := layouts[j]
			if other.Pool != "" || other.Disk != "" || other.Filesystem == layouts[i].Filesystem {
				continue
			}
			if sameSpace(other, layouts[i]) {
				layouts[i].Filesystem = other.Filesystem
				layouts[i].Evidence = "same size and free space as " + other.Path + ": probably one volume"
				break
			}
		}
	}
}

func sameSpace(a, b Layout) bool {
	if a.TotalBytes != b.TotalBytes {
		return false
	}
	difference := a.FreeBytes - b.FreeBytes
	if difference < 0 {
		difference = -difference
	}
	return difference*200 <= a.TotalBytes
}
