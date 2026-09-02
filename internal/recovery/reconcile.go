// Package recovery runs the pass ADR-15 requires after a database is restored from a
// backup, before any worker starts. It composes library.db and jobs.db in Go, because
// no query may join across database files (section 4.3).
package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/store"
)

// EventKind is the event_log kind a restore is recorded under. ADR-15 calls for a
// persistent notification; the durable event log is the only one that exists.
const EventKind = "database.restored"

// jobFailureReason lands in job.last_error, where a user reads it, so it explains
// itself rather than pointing at a document.
const jobFailureReason = "failed by post-restore reconciliation: the database was restored " +
	"from a backup and this job may reference rows that no longer exist"

// Known is the configuration a restored library must still resolve against. It comes
// from settings.db, which library.db references by id with no foreign key (ADR-25).
type Known struct {
	CategoryIDs   []int64
	RootFolderIDs []int64
}

// OrphanedReference is a library row pointing at configuration that is no longer
// there -- the realistic result of restoring one database and not the other.
type OrphanedReference struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	ID     int64  `json:"id"`
	Rows   int64  `json:"rows"`
}

// Gap is something ADR-15 asks for that this stage cannot do yet, named rather than
// stubbed so it appears in the record instead of looking finished.
type Gap struct {
	What    string `json:"what"`
	Because string `json:"because"`
}

// RestoredFrom is one database's recovery, in the shape the event log stores. It
// mirrors store.RecoveryReport, which stays free of the payload's encoding.
type RestoredFrom struct {
	Database           string    `json:"database"`
	Backup             string    `json:"backup"`
	BackupAt           time.Time `json:"backup_at"`
	HistoryLostSeconds int64     `json:"history_lost_seconds"`
	CorruptPath        string    `json:"corrupt_path,omitempty"`
	RejectedBackups    []string  `json:"rejected_backups,omitempty"`
}

// Report is what the reconciliation did. It is also the event log's payload, which is
// what makes the notification survive the restart that produced it.
type Report struct {
	Restored            []RestoredFrom      `json:"restored"`
	JobsFailed          int64               `json:"jobs_failed"`
	DesiredItemsDirtied int64               `json:"desired_items_dirtied"`
	Orphans             []OrphanedReference `json:"orphans,omitempty"`
	Gaps                []Gap               `json:"gaps"`
}

// unbuildable is what ADR-15 names that no subsystem exists to do yet. Downloader
// clients arrive at stage 5 and the planner at stage 6.
var unbuildable = []Gap{
	{
		What:    "re-derive in-flight acquisitions from downloader state",
		Because: "no downloader client exists yet (stage 5)",
	},
	{
		What:    "check candidate.indexer_id and acquisition.downloader_id",
		Because: "indexers and downloaders are not configurable yet (stage 5)",
	},
}

// Reconcile fails unfinished jobs, marks the library for re-planning, reports the
// configuration references the restore orphaned, and records all of it (ADR-15). It
// runs before workers start, so nothing contends with it.
func Reconcile(
	ctx context.Context,
	cl clock.Clock,
	libraryStore, jobsStore *store.Store,
	known Known,
	causes []store.RecoveryReport,
) (Report, error) {
	report := Report{Gaps: unbuildable}
	for _, cause := range causes {
		report.Restored = append(report.Restored, RestoredFrom{
			Database:           cause.Database,
			Backup:             cause.From,
			BackupAt:           cause.BackupAt,
			HistoryLostSeconds: int64(cause.HistoryLost(cl.Now()).Seconds()),
			CorruptPath:        cause.CorruptPath,
			RejectedBackups:    cause.Rejected,
		})
	}

	queue := jobs.NewQueue(jobsStore, cl, jobs.NewRegistry(), nil)
	failed, err := queue.FailUnfinished(ctx, jobFailureReason)
	if err != nil {
		return report, err
	}
	report.JobsFailed = failed

	restored := library.NewRestored(libraryStore)
	dirtied, err := restored.MarkEverythingDirty(ctx)
	if err != nil {
		return report, err
	}
	report.DesiredItemsDirtied = dirtied

	orphans, err := findOrphans(ctx, restored, known)
	if err != nil {
		return report, err
	}
	report.Orphans = orphans

	encoded, err := json.Marshal(report)
	if err != nil {
		return report, fmt.Errorf("recovery: encode the restore report: %w", err)
	}
	_, err = jobs.NewHistory(jobsStore, cl).Append(ctx, jobs.Event{
		Kind: EventKind, SubjectType: "database", Data: string(encoded),
	})
	if err != nil {
		return report, err
	}
	return report, nil
}

// findOrphans reports the references a restore broke rather than repairing them:
// which category a library row should point at instead is a question only the user
// can answer (ADR-25).
func findOrphans(ctx context.Context, restored library.Restored, known Known) ([]OrphanedReference, error) {
	categories, err := restored.CategoryReferences(ctx)
	if err != nil {
		return nil, err
	}
	roots, err := restored.RootFolderReferences(ctx)
	if err != nil {
		return nil, err
	}

	var orphans []OrphanedReference
	for _, reference := range categories {
		if !slices.Contains(known.CategoryIDs, reference.ID) {
			orphans = append(orphans, OrphanedReference{
				Table: "desired_item", Column: "category_id", ID: reference.ID, Rows: reference.Rows,
			})
		}
	}
	for _, reference := range roots {
		if !slices.Contains(known.RootFolderIDs, reference.ID) {
			orphans = append(orphans, OrphanedReference{
				Table: "library_path", Column: "root_folder_id", ID: reference.ID, Rows: reference.Rows,
			})
		}
	}
	return orphans, nil
}
