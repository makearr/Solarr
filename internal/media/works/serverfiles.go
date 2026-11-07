package works

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/sidecar"
)

// serverFilesPage bounds one read of the works on a root.
const serverFilesPage = 200

// ServerFilesPass is the pass that writes Jellyfin and Kodi files for a media type: every work of each library that
// asks for them, or the one work a payload names. Write is the type's own part, answering how many files it wrote.
type ServerFilesPass struct {
	MediaType string
	// Noun names the works in log lines, in the singular ("movie", "series").
	Noun   string
	Reader interface {
		WorksOnRoot(ctx context.Context, mediaType string, rootFolderID, afterID, pageSize int64) ([]int64, error)
	}
	Write func(ctx context.Context, categoryID, workID int64) (int, error)
	// Libraries answers every library, with the roots each is on.
	Libraries func() []settings.Category
	RootsOf   func(categoryID int64) []settings.RootFolder
	Logger    *slog.Logger
}

// serverFilesPayload names the one work a pass is for; empty is every work.
type serverFilesPayload struct {
	CategoryID int64 `json:"category_id"`
	WorkID     int64 `json:"work_id"`
}

// Run implements jobs.Handler.
func (p ServerFilesPass) Run(ctx context.Context, job jobs.Job) error {
	var one serverFilesPayload
	if err := json.Unmarshal(job.Payload, &one); err == nil && one.WorkID != 0 {
		return p.runOne(ctx, one)
	}
	written, count := 0, 0
	for _, category := range p.Libraries() {
		if category.MediaType != p.MediaType || !category.FileManagement.MediaServerFiles {
			continue
		}
		for _, root := range p.RootsOf(category.ID) {
			if !sidecar.WritableRoot(root) {
				continue
			}
			var afterID int64
			for {
				ids, err := p.Reader.WorksOnRoot(ctx, p.MediaType, root.ID, afterID, serverFilesPage)
				if err != nil {
					return err
				}
				for _, workID := range ids {
					afterID = workID
					made, err := p.Write(ctx, category.ID, workID)
					if err != nil {
						p.Logger.Warn("jellyfin/kodi files of a "+p.Noun+" failed", slog.Int64("work_id", workID), slog.Any("error", err))
						continue
					}
					written, count = written+made, count+1
				}
				if len(ids) < serverFilesPage {
					break
				}
			}
		}
	}
	p.Logger.Info("jellyfin and kodi files written", slog.String("media_type", p.MediaType), slog.Int("files", written), slog.Int(p.Noun, count))
	return nil
}

func (p ServerFilesPass) runOne(ctx context.Context, one serverFilesPayload) error {
	for _, category := range p.Libraries() {
		if category.ID != one.CategoryID || category.MediaType != p.MediaType || !category.FileManagement.MediaServerFiles {
			continue
		}
		made, err := p.Write(ctx, one.CategoryID, one.WorkID)
		if err != nil {
			return err
		}
		p.Logger.Info("jellyfin and kodi files written", slog.Int("files", made), slog.Int64("work_id", one.WorkID))
	}
	return nil
}

// ServerFilesDefinition declares the pass: housekeeping, never in front of anything a person waits on.
func ServerFilesDefinition(name string, jobType jobs.Type) jobs.Definition {
	return jobs.Definition{Name: name, Type: jobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 2}
}

// EnqueueServerFilesFor submits a pass for one work, as right after an import; one already waiting for it is the same work.
func EnqueueServerFilesFor(ctx context.Context, queue *jobs.Queue, jobType jobs.Type, categoryID, workID int64) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(serverFilesPayload{CategoryID: categoryID, WorkID: workID})
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: jobType, ActionKey: fmt.Sprintf("%s:%d", jobType, workID), Priority: jobs.PriorityLow, Payload: payload,
	})
}

// EnqueueServerFiles submits one pass over every work; a second while one waits is the same work.
func EnqueueServerFiles(ctx context.Context, queue *jobs.Queue, jobType jobs.Type) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: jobType, ActionKey: string(jobType), Priority: jobs.PriorityLow, Payload: []byte("{}")})
}
