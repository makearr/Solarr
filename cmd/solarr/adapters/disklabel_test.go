package adapters

import (
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestGroupLabelNamesGroupsReadably(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	manager := &DiskManager{Clock: fake}
	manager.cached = []apiserver.DiskPath{
		{Path: "/music", MountPoint: "/mnt/music", FSType: "ext4", DetectedGroup: "fs:0:260", Group: "fs:0:260"},
		{Path: "/films", MountPoint: "/mnt/films", FSType: "nfs4", DetectedGroup: "fs:0:300", Group: "nvme", Overridden: true},
		{Path: "/pool", MountPoint: "/tank", FSType: "zfs", DetectedGroup: "zfs:tank", Group: "zfs:tank"},
	}
	manager.cachedAt = fake.Now()
	manager.pathGroups = map[string]pathGroup{"/cfg": {group: "fs:0:400", fallback: "xfs /config"}}
	want := map[string]string{
		"fs:0:260": "ext4 /mnt/music", "fs:0:300": "nvme", "nvme": "nvme", "zfs:tank": "zfs:tank",
		"fs:0:400": "xfs /config", "fs:0:999": "fs:0:999",
	}
	for group, label := range want {
		if got := manager.GroupLabel(group); got != label {
			t.Errorf("GroupLabel(%q) = %q, want %q", group, got, label)
		}
	}
}
