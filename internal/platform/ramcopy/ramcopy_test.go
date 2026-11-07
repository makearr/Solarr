package ramcopy

import (
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestSync_CopiesChangesAndRemovesWhatIsGone(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	put := func(path, content string) {
		t.Helper()
		if err := fsys.MkdirAll(dirOf(path), 0o755); err != nil {
			t.Fatal(err)
		}
		file, err := fsys.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte(content))
		_ = file.Close()
	}
	put("/ram/a.webp", "one")
	put("/ram/sub/b.webp", "two")
	put("/disk/gone.webp", "stale")

	copied, removed, err := Sync(t.Context(), fsys, "/ram", "/disk")
	if err != nil || copied != 2 || removed != 1 {
		t.Fatalf("first sync: copied %d removed %d err %v, want 2, 1", copied, removed, err)
	}
	copied, removed, err = Sync(t.Context(), fsys, "/ram", "/disk")
	if err != nil || copied != 0 || removed != 0 {
		t.Fatalf("second sync: copied %d removed %d err %v, want nothing to do", copied, removed, err)
	}
	fake.Advance(time.Minute)
	put("/ram/a.webp", "changed!")
	if copied, _, _ := Sync(t.Context(), fsys, "/ram", "/disk"); copied != 1 {
		t.Errorf("a changed file was copied %d times, want 1", copied)
	}
	bytes, err := DirBytes(t.Context(), fsys, "/disk")
	if err != nil || bytes != int64(len("changed!")+len("two")) {
		t.Errorf("DirBytes = %d, %v", bytes, err)
	}
}

func dirOf(path string) string { return path[:strings.LastIndex(path, "/")] }

func TestWithHeadroom_AddsTenPercent(t *testing.T) {
	if got := WithHeadroom(1000); got != 1101 {
		t.Errorf("WithHeadroom(1000) = %d, want 1101", got)
	}
}

func TestReadMemory_UsesTheCgroupLimitAndLeavesOutReclaimableCache(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	files := map[string]string{
		cgroupV2Max:     "4294967296\n",
		cgroupV2Current: "2147483648\n",
		cgroupV2Stat:    "anon 100\ninactive_file 1073741824\n",
	}
	for path, content := range files {
		if err := fsys.MkdirAll(dirOf(path), 0o755); err != nil {
			t.Fatal(err)
		}
		file, _ := fsys.Create(path)
		_, _ = file.Write([]byte(content))
		_ = file.Close()
	}
	memory, err := ReadMemory(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if memory.LimitBytes != 4<<30 || memory.UsedBytes != 1<<30 || memory.AvailableBytes != 3<<30 || memory.Source != "cgroup v2" {
		t.Errorf("memory = %+v", memory)
	}
}

func TestReadMemory_FallsBackToTheHostWhenTheContainerHasNoLimit(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	for path, content := range map[string]string{cgroupV2Max: "max\n", procMeminfo: "MemTotal: 8388608 kB\nMemAvailable: 4194304 kB\n"} {
		_ = fsys.MkdirAll(dirOf(path), 0o755)
		file, _ := fsys.Create(path)
		_, _ = file.Write([]byte(content))
		_ = file.Close()
	}
	memory, err := ReadMemory(fsys)
	if err != nil || memory.LimitBytes != 0 || memory.AvailableBytes != 4<<30 {
		t.Errorf("memory = %+v, err %v", memory, err)
	}
}
