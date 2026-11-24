package transcoder

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Items is the judged files; library.TranscodeItems is the real one.
type Items interface {
	Judge(ctx context.Context, judged []library.JudgedTranscodeItem) error
	ObjectsToAnalyze(ctx context.Context, rootIDs []int64, targetID, setupRevision, afterID, pageSize int64) ([]library.ObjectToAnalyze, error)
	Facts(ctx context.Context, ids []int64) (map[int64]library.ObjectFacts, error)
}

// Settings is where the pipeline reads its policy.
type Settings interface {
	Global() settings.TranscodeGlobal
	Libraries(ctx context.Context) ([]settings.TranscodeLibrary, error)
	Setup(ctx context.Context, targetID int64) (settings.StoredTranscodeSetup, error)
	RootFolders(categoryID int64) []settings.RootFolder
	// Languages are the audio languages the library prefers, two-letter.
	Languages(categoryID int64) []string
}

// Queue is where the pipeline submits its jobs; *jobs.Queue is the real one.
type Queue interface {
	Enqueue(ctx context.Context, request jobs.Request) (jobs.EnqueueResult, error)
}

// Service is the conversion pipeline; every dependency is an interface so the whole of it runs on fakes.
type Service struct {
	Items    Items
	Settings Settings
	Hardware Hardware
	Queue    Queue
	Clock    clock.Clock
	Logger   *slog.Logger
}
