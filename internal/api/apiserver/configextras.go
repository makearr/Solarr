package apiserver

import (
	"context"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// ConfigExtras carries what the configuration document holds beyond the settings snapshot: webhooks and definition files.
// cmd/solarr/app implements it; Check runs before the settings import and Apply after it succeeded.
type ConfigExtras interface {
	Export(ctx context.Context, document *settings.Document) error
	Check(ctx context.Context, document settings.Document) config.Problems
	Apply(ctx context.Context, document settings.Document) error
}
