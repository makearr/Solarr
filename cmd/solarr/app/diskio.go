package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/disklatency"
	"github.com/makearr/Solarr/internal/platform/iogate"
)

const (
	// unknownDiskGroup holds every job whose disk is not named in its payload.
	unknownDiskGroup = "*"
	// diskSlots is how many jobs use one disk at once; one of them is always kept free of bulk work.
	diskSlots = 2
	// unknownDiskSlots is wider because the group mixes several disks.
	unknownDiskSlots = 4
	// slowDiskLatency and stalledDiskLatency shrink a group's slots: a disk that answers slowly is already saturated.
	slowDiskLatency    = 250 * time.Millisecond
	stalledDiskLatency = time.Second
)

// jobClasses ranks the job types that read or write library disks; a type not listed is not held back.
var jobClasses = map[jobs.Type]iogate.Class{
	"import.run": iogate.ClassImport, "import.inspect": iogate.ClassImport,
	"import.place_unplaced": iogate.ClassImport, "import.download_watch": iogate.ClassImport,
	"library.rename_automatic": iogate.ClassRename, "tv.rename_titled": iogate.ClassRename,
	"music.tag_writeback": iogate.ClassTagWriteback,
	"move.apply":          iogate.ClassDrain, "link.identical": iogate.ClassDrain,
	"library.scan": iogate.ClassScan, "library.rescan": iogate.ClassScan,
	"library.probe":               iogate.ClassProbe,
	"library.statistics":          iogate.ClassStatistics,
	"library.recycle_bin_cleanup": iogate.ClassRecycleBin,
	"library.video_pictures":      iogate.ClassPreviews, "library.preview_cleanup": iogate.ClassPreviews,
}

// diskTurns makes each disk-bound job wait for its turn on its disk.
type diskTurns struct {
	gate    *iogate.Gate
	disks   *adapters.DiskManager
	monitor *disklatency.Monitor
}

func newDiskTurns(disks *adapters.DiskManager, monitor *disklatency.Monitor, c clock.Clock) *diskTurns {
	turns := &diskTurns{disks: disks, monitor: monitor}
	turns.gate = iogate.New(c, iogate.Options{Capacity: turns.slots, OnWait: turns.measure})
	return turns
}

// Acquire is jobs.DiskGate.
func (d *diskTurns) Acquire(ctx context.Context, job jobs.Job) (func(), error) {
	class, gated := jobClasses[job.Type]
	if !gated {
		return func() {}, nil
	}
	return d.gate.Acquire(ctx, d.groupOf(job), class)
}

// groupOf is the disk a job works on: its root folder's group when the payload names one.
func (d *diskTurns) groupOf(job jobs.Job) string {
	var named struct {
		RootFolderID int64 `json:"root_folder_id"`
	}
	if json.Unmarshal(job.Payload, &named) == nil && named.RootFolderID != 0 {
		if group, _, found := d.disks.GroupOfRoot(named.RootFolderID); found {
			return group
		}
	}
	return unknownDiskGroup
}

// slots is the group's width, narrowed while its disk answers slowly.
func (d *diskTurns) slots(group string) int {
	base := diskSlots
	if group == unknownDiskGroup {
		base = unknownDiskSlots
	}
	latency, measured := d.monitor.Latest(group)
	switch {
	case !measured:
		return base
	case latency >= stalledDiskLatency:
		return 1
	case latency >= slowDiskLatency:
		return max(1, base/2)
	default:
		return base
	}
}

// rootIdleWindow is how long a root's disk must have gone unused before the scheduled polls leave it alone.
const rootIdleWindow = 30 * time.Minute

// rootIdle is true when polling the root would only wake its disk: it has been read once and nothing has used it since.
func (d *diskTurns) rootIdle(root settings.RootFolder) bool {
	if root.CapacityCheckedAt == nil {
		return false
	}
	group, _, found := d.disks.GroupOfRoot(root.ID)
	return found && d.gate.Idle(group, rootIdleWindow)
}

// measure times the group's disk now that a job waits for it; the monitor rate limits repeated asks.
func (d *diskTurns) measure(group string) {
	if path, found := d.disks.PathOfGroup(group); found {
		go d.monitor.Probe(group, path)
	}
}
