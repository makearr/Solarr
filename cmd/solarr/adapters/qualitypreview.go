package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// qualityPreviewPage is how many items one read of a media type's checkable items takes.
const qualityPreviewPage = 200

// QualityPreview satisfies apiserver.QualityPreviewer over the media type's own cutoff judgement.
type QualityPreview struct {
	Cache    *settings.Cache
	Registry *media.Registry
}

func (q QualityPreview) Preview(ctx context.Context, categoryID int64, change apiserver.QualityChange) (apiserver.QualityChangePreview, error) {
	snapshot := q.Cache.Snapshot()
	current, found := snapshot.Category(categoryID)
	if !found {
		return apiserver.QualityChangePreview{Reason: "the library is gone"}, nil
	}
	if change.TargetAuto {
		return apiserver.QualityChangePreview{Reason: "an automatic target is worked out when it is saved"}, nil
	}
	previewing, ok := media.Capability[cutoff.Previewing](q.Registry, media.Key(current.MediaType))
	if !ok {
		return apiserver.QualityChangePreview{Reason: "this media type cannot preview a quality change"}, nil
	}
	proposed := current
	proposed.QualityProfileIDs = change.ProfileIDs
	proposed.QualityCutoffID = change.CutoffProfileID
	proposed.QualityTarget = nil
	if change.TargetID != nil {
		target, found := snapshot.QualityTarget(*change.TargetID)
		if !found {
			return apiserver.QualityChangePreview{Reason: "that target no longer exists"}, nil
		}
		proposed.QualityTarget = &target
	}
	if change.UpgradesAllowed != nil {
		proposed.QualityUpgrades = *change.UpgradesAllowed
	}
	preview := apiserver.QualityChangePreview{Available: true}
	var after int64
	for {
		page, err := previewing.CutoffPage(ctx, 0, after, qualityPreviewPage)
		if err != nil {
			return apiserver.QualityChangePreview{}, err
		}
		for _, item := range page {
			after = item.ID
			if item.CategoryID != categoryID {
				continue
			}
			if err := countUnder(ctx, previewing, snapshot, current, proposed, item, &preview); err != nil {
				return apiserver.QualityChangePreview{}, err
			}
		}
		if int64(len(page)) < qualityPreviewPage {
			return preview, nil
		}
	}
}

// countUnder adds one item to the counts when it can be judged under both the library's quality now and as proposed.
func countUnder(
	ctx context.Context, previewing cutoff.Previewing, snapshot *settings.Snapshot, current, proposed settings.Category,
	item library.CutoffItem, preview *apiserver.QualityChangePreview,
) error {
	now, decidedNow, err := previewing.StateUnder(ctx, snapshot, current, item)
	if err != nil || !decidedNow {
		return err
	}
	then, decidedThen, err := previewing.StateUnder(ctx, snapshot, proposed, item)
	if err != nil || !decidedThen {
		return err
	}
	const upgradable = "UPGRADABLE"
	preview.Items++
	switch {
	case now == upgradable && then == upgradable:
		preview.UpgradableNow++
		preview.UpgradableAfter++
	case now == upgradable:
		preview.UpgradableNow++
		preview.StopUpgradable++
	case then == upgradable:
		preview.UpgradableAfter++
		preview.BecomeUpgradable++
	}
	return nil
}
