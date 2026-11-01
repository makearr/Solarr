package privilege

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGPUGroups_KeepsTheDevicesGroupOnlyWhenItIsNeitherRootNorThePrimary(t *testing.T) {
	if os.Getgid() == 0 {
		t.Skip("a root-owned node proves nothing")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "renderD128"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	own := os.Getgid()

	if got := gpuGroups(dir, own+1); !slices.Equal(got, []int{own}) {
		t.Errorf("gpuGroups with another primary = %v, want [%d]", got, own)
	}
	if got := gpuGroups(dir, own); len(got) != 0 {
		t.Errorf("gpuGroups with the node's group as primary = %v, want none", got)
	}
	if got := gpuGroups(filepath.Join(dir, "absent"), own); len(got) != 0 {
		t.Errorf("gpuGroups of a missing directory = %v, want none", got)
	}
}
