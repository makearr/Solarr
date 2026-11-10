package disktopo

import (
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

const sampleMountinfo = `25 1 0:23 / / rw - overlay overlay rw
40 25 8:1 / /mnt/media rw - ext4 /dev/sda1 rw
41 25 8:2 / /mnt/tv rw - ext4 /dev/sda2 rw
42 25 0:50 / /mnt/zmovies rw shared:1 - zfs tank/movies rw
43 25 0:51 / /mnt/zseries rw - zfs tank/series rw
44 25 259:1 / /config rw - ext4 /dev/nvme0n1p1 rw
45 25 0:60 / /mnt/nas\040share rw - nfs4 nas:/export/a rw
`

func writeFile(t *testing.T, fsys *fsx.Mem, path, content string) {
	t.Helper()
	if err := fsys.MkdirAll(path[:strings.LastIndex(path, "/")], 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte(content))
	_ = file.Close()
}

func TestParseMountinfo(t *testing.T) {
	got, err := ParseMountinfo(strings.NewReader(sampleMountinfo))
	if err != nil {
		t.Fatal(err)
	}
	want := Mount{DeviceID: "0:50", MountPoint: "/mnt/zmovies", FSType: "zfs", Source: "tank/movies"}
	if len(got) != 7 || got[3] != want {
		t.Fatalf("got %d mounts, mount 3 = %+v, want 7 and %+v", len(got), got[3], want)
	}
	if got[6].MountPoint != "/mnt/nas share" {
		t.Fatalf("octal escape not undone: %q", got[6].MountPoint)
	}
}

func TestDetect_GroupsByDiskPoolAndNetwork(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/proc/self/mountinfo", sampleMountinfo)
	for _, dir := range []string{"/mnt/media/m", "/mnt/tv/s", "/mnt/zmovies/m", "/mnt/zseries/s", "/config/c", "/mnt/nas share/x"} {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// sda1 and sda2 are partitions of sda; the config volume is a partition of an NVMe disk.
	for device, disk := range map[string]string{"8:1": "sda", "8:2": "sda", "259:1": "nvme0n1"} {
		partition := "/sys/devices/pci/block/" + disk + "/" + disk + "p"
		if err := fsys.MkdirAll(partition, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, fsys, partition+"/partition", "1")
		if err := fsys.MkdirAll("/sys/dev/block", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Symlink(partition, "/sys/dev/block/"+device); err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{"/mnt/media/m", "/mnt/tv/s", "/mnt/zmovies/m", "/mnt/zseries/s", "/config/c", "/mnt/nas share/x"}
	var groups, filesystems []string
	for _, layout := range Detect(fsys, paths) {
		groups = append(groups, layout.Group())
		filesystems = append(filesystems, layout.Filesystem)
	}
	wantGroups := []string{"disk:sda", "disk:sda", "zfs:tank", "zfs:tank", "disk:nvme0n1", "net:nas"}
	if !reflect.DeepEqual(groups, wantGroups) {
		t.Fatalf("groups = %v, want %v", groups, wantGroups)
	}
	wantFilesystems := []string{"8:1", "8:2", "0:50", "0:51", "259:1", "0:60"}
	if !reflect.DeepEqual(filesystems, wantFilesystems) {
		t.Fatalf("filesystems = %v, want %v", filesystems, wantFilesystems)
	}
}

func TestDetect_WithoutMountTableKnowsOnlyTheFilesystemNumber(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/data", 0o755); err != nil {
		t.Fatal(err)
	}
	got := Detect(fsys, []string{"/data"})[0]
	if got.Group() != "fs:mem" || got.Evidence == "" {
		t.Fatalf("got group %q evidence %q, want fs:mem with an explanation", got.Group(), got.Evidence)
	}
}

func TestSameSpace_JoinsOnlyWhatLooksLikeOneVolume(t *testing.T) {
	a := Layout{Path: "/a", Filesystem: "0:1", TotalBytes: 1000, FreeBytes: 500}
	b := Layout{Path: "/b", Filesystem: "0:2", TotalBytes: 1000, FreeBytes: 502}
	c := Layout{Path: "/c", Filesystem: "0:3", TotalBytes: 1000, FreeBytes: 900}
	layouts := []Layout{a, b, c}
	groupBySpace(layouts)
	if layouts[1].Group() != "fs:0:1" || layouts[2].Group() != "fs:0:3" {
		t.Fatalf("groups = %q %q %q, want b joined to a and c apart", layouts[0].Group(), layouts[1].Group(), layouts[2].Group())
	}
}
