package adapters

import (
	"context"
	"sort"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/linkjob"
)

// Links satisfies apiserver.LinkManager over linkjob.
type Links struct {
	Config linkjob.Config
	Queue  *jobs.Queue
}

// LinkRoots are a library's roots a link may be made on: online, writable and not read-only.
func LinkRoots(cache *settings.Cache) linkjob.Roots {
	return func(categoryID int64) []linkjob.Root {
		var roots []linkjob.Root
		for _, folder := range cache.Snapshot().RootFoldersForCategory(categoryID) {
			if folder.Online && folder.Writable && !folder.ReadOnly {
				roots = append(roots, linkjob.Root{ID: folder.ID, Path: folder.Path})
			}
		}
		return roots
	}
}

func (l Links) PreviewLinks(ctx context.Context, categoryID int64) (apiserver.LinkPreview, error) {
	plan, err := l.Config.PlanFor(ctx, categoryID)
	if err != nil {
		return apiserver.LinkPreview{}, err
	}
	preview := apiserver.LinkPreview{Total: len(plan.Links), ReclaimableBytes: plan.ReclaimableBytes}
	for _, link := range plan.Links {
		preview.Links = append(preview.Links, apiserver.LinkRow{
			MediaObjectID: link.MediaObjectID, SizeBytes: link.SizeBytes, Keep: link.Keep.Abs, Replace: link.Replace.Abs,
			Method: string(link.Method),
		})
	}
	counts := map[string]int{}
	for _, skipped := range plan.Skipped {
		counts[skipped.Reason]++
	}
	for reason, count := range counts {
		preview.Skipped = append(preview.Skipped, apiserver.LinkSkip{Reason: reason, Count: count})
	}
	sort.Slice(preview.Skipped, func(a, b int) bool { return preview.Skipped[a].Reason < preview.Skipped[b].Reason })
	return preview, nil
}

func (l Links) StartLinks(ctx context.Context, categoryID int64) error {
	_, err := linkjob.Enqueue(ctx, l.Queue, categoryID)
	return err
}
