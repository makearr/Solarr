package adapters

import (
	"context"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// FileSettingPreviews implements apiserver.FileSettingPreviews through the library's media type.
type FileSettingPreviews struct {
	Cache    *settings.Cache
	Registry *media.Registry
}

// Preview implements apiserver.FileSettingPreviews.
func (a FileSettingPreviews) Preview(ctx context.Context, categoryID int64, key, option string) (int, error) {
	category, found := a.Cache.Snapshot().Category(categoryID)
	if !found {
		return 0, apiserver.ErrSeriesNotFound
	}
	previewing, found := media.Capability[media.FilePreviewing](a.Registry, media.Key(category.MediaType))
	if !found || !slices.Contains(previewing.PreviewableSettings(), media.FileSettingKey(key)) {
		return 0, fmt.Errorf("%w: this library cannot preview %q", apiserver.ErrInvalidChoice, key)
	}
	return previewing.PreviewFileSetting(ctx, categoryID, media.FileSettingKey(key), option)
}
