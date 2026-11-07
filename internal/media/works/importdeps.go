package works

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ImportDeps is what a one-work-per-unit type's batch importer (movie, book, adult) is handed besides its resolver.
// Logger, Exclusions, RenameOnImport, ExtraFiles and AfterPlacing are optional.
type ImportDeps struct {
	FS           fsx.FS
	Placer       placer.Placer
	Physical     library.Physical
	Series       library.Series
	Reader       library.SeriesReader
	Identity     library.Identity
	Desired      library.Desired
	Acquisitions batch.AcquisitionSource
	Categories   interface {
		Category(id int64) (settings.Category, bool)
	}
	// Exclusions lifts a work's exclusion when a person names it for a held unit.
	Exclusions interface {
		Allow(ctx context.Context, mediaType, provider, externalID string) error
	}
	Clock          clock.Clock
	Logger         *slog.Logger
	RenameOnImport func() bool
	// ExtraFiles lists the extensions of files brought along with the work they are named after; nil brings none.
	ExtraFiles func(categoryID int64) []string
	// AfterPlacing is told of each work an import placed files of.
	AfterPlacing func(ctx context.Context, categoryID, workID int64)
}

// CategoryNaming answers a library's naming profile (nil when it has none) and file management for an import, and
// whether the install-wide switch lets files be renamed on import at all (nil switch: yes).
func CategoryNaming(categories interface {
	Category(id int64) (settings.Category, bool)
}, categoryID int64, renameOnImport func() bool,
) (profile *settings.NamingProfile, fileManagement settings.FileManagement, mayRename bool) {
	fileManagement = settings.DefaultFileManagement
	if category, found := categories.Category(categoryID); found {
		profile, fileManagement = category.Naming, category.FileManagement
	}
	return profile, fileManagement, renameOnImport == nil || renameOnImport()
}
