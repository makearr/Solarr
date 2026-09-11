package apiserver

import (
	"context"
	"net/http"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/jobs"
)

// LibrarySize reads the counter table, which is where every library figure comes
// from (section 21.12, ADR-28). Defined here and implemented by the caller, so
// this package never imports the library store.
type LibrarySize interface {
	// Counters returns metric to value for one scope. A metric that has never
	// been counted is simply absent.
	Counters(ctx context.Context, scope string) (map[string]int64, error)
}

// libraryScope is the counter scope holding whole-library totals.
const libraryScope = "library"

// GetDashboard answers the landing page in one call (section 21.12).
func (s *Server) GetDashboard(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}

	summary := gen.DashboardSummary{
		RootFolders: s.rootFolderViews(),
		Jobs:        s.jobCounts(r.Context()),
		Library:     s.libraryCounts(r.Context()),
	}
	writeJSON(w, s.logger, http.StatusOK, summary)
}

// rootFolderViews is shared with ListRootFolders so the dashboard and the
// settings page can never disagree about whether a root is writable.
func (s *Server) rootFolderViews() []gen.RootFolder {
	folders := s.auth.Settings.Snapshot().RootFolders()
	views := make([]gen.RootFolder, 0, len(folders))
	for _, folder := range folders {
		view := s.rootFolderView(folder)
		if s.roots != nil {
			writable := s.roots.Writable(folder.Path) == nil
			view.Writable = &writable
		}
		views = append(views, view)
	}
	return views
}

// jobCounts folds the queue's per-state, per-pool counts into what a tile shows.
// PENDING_RETRY is waiting and LEASED is claimed, so each joins the state a
// reader would expect rather than appearing as a category of its own.
func (s *Server) jobCounts(ctx context.Context) gen.JobCounts {
	var counts gen.JobCounts
	if s.jobs == nil {
		return counts
	}

	byState, err := s.jobs.Counts(ctx)
	if err != nil {
		// A dashboard that renders without its job tile beats one that 500s.
		s.logger.WarnContext(ctx, "job counts unavailable", "error", err)
		return counts
	}

	for state, byPool := range byState {
		total := 0
		for _, count := range byPool {
			total += int(count)
		}
		switch state {
		case jobs.StatePending, jobs.StatePendingRetry:
			counts.Pending += total
		case jobs.StateRunning, jobs.StateLeased:
			counts.Running += total
		case jobs.StateFailed:
			counts.Failed += total
		case jobs.StateSucceeded:
			counts.Succeeded += total
		case jobs.StateCancelled:
			counts.Cancelled += total
		}
		counts.Total += total
	}
	return counts
}

func (s *Server) libraryCounts(ctx context.Context) gen.LibraryCounts {
	var counts gen.LibraryCounts
	if s.librarySize == nil {
		return counts
	}

	values, err := s.librarySize.Counters(ctx, libraryScope)
	if err != nil {
		s.logger.WarnContext(ctx, "library counters unavailable", "error", err)
		return counts
	}

	counts.Works = int(values["works"])
	counts.Files = int(values["files"])
	counts.Monitored = int(values["monitored"])
	counts.Missing = int(values["missing"])
	// probed/total_files are library.ProbeCoverage's own metric pair (todo 4.2),
	// reconciled by a separate sweep into the same "library" counter scope --
	// distinct metrics, not a collision.
	counts.Probed = int(values["probed"])
	counts.Unprobed = int(values["total_files"] - values["probed"])
	counts.Fresh = int(values["fresh"])
	return counts
}
