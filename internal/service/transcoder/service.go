package transcoder

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// Items is the judged files; library.TranscodeItems is the real one.
type Items interface {
	Judge(ctx context.Context, judged []library.JudgedTranscodeItem) error
	ObjectsToAnalyze(ctx context.Context, rootIDs []int64, targetID, setupRevision, afterID, pageSize int64) ([]library.ObjectToAnalyze, error)
	Facts(ctx context.Context, ids []int64) (map[int64]library.ObjectFacts, error)
	Get(ctx context.Context, mediaObjectID int64) (library.TranscodeItem, bool, error)
	Transition(ctx context.Context, mediaObjectID int64, from, to library.TranscodeState, mutate func(*library.TranscodeItem)) error
	SetJudgement(ctx context.Context, mediaObjectID int64, reasonsJSON, planJSON, warningsJSON string) error
	SetJournal(ctx context.Context, mediaObjectID int64, stage, workPath string, recycleID int64) error
	Paths(ctx context.Context, mediaObjectID int64) ([]library.ObjectPath, error)
	ReplaceBytes(ctx context.Context, replaced library.ReplacedFile) error
	Pick(ctx context.Context, order library.TranscodeOrder, categoryID int64, kind string, limit int64) ([]library.TranscodePick, error)
	PickFarAway(ctx context.Context, categoryID int64, kind string, avoidRootID int64, avoidPrefix string, limit int64) ([]library.TranscodePick, error)
	Running(ctx context.Context) ([]library.TranscodeItem, error)
	Journalled(ctx context.Context) ([]library.TranscodeItem, error)
	Health(ctx context.Context, kind string) (library.TranscodeHealth, error)
	SaveHealth(ctx context.Context, health library.TranscodeHealth) error
	List(ctx context.Context, filter library.TranscodeFilter) ([]library.TranscodeListed, error)
	RestoreBytes(ctx context.Context, restored library.RestoredFile) error
	OriginalsKept(ctx context.Context) (int64, error)
	OldestOriginals(ctx context.Context, limit int64) ([]library.KeptOriginal, error)
}

// Settings is where the pipeline reads its policy.
type Settings interface {
	Global() settings.TranscodeGlobal
	Libraries(ctx context.Context) ([]settings.TranscodeLibrary, error)
	Setup(ctx context.Context, targetID int64) (settings.StoredTranscodeSetup, error)
	RootFolders(categoryID int64) []settings.RootFolder
	RootFolder(id int64) (settings.RootFolder, bool)
	// Roots are every configured root folder.
	Roots() []settings.RootFolder
	// Languages are the audio languages the library prefers, two-letter.
	Languages(categoryID int64) []string
	// Retention is how long the library keeps what its recycle bin takes.
	Retention(categoryID int64) time.Duration
}

// Prober reads every stream of a file; *transcode.Service is the real one.
type Prober interface {
	ProbeStreams(ctx context.Context, path string) (transcode.File, error)
}

// Tagger reads and writes the tags ffmpeg cannot carry through an MP4 that holds a cover picture.
type Tagger interface {
	// WriteTags sets the tags as freeform atoms.
	WriteTags(ctx context.Context, path string, tags map[string]string) error
	// Marker is the conversion marker the file carries, empty for none.
	Marker(ctx context.Context, path string) string
}

// Bin is the recycle bin; library.RecycleBin is the real one.
type Bin interface {
	Put(ctx context.Context, request library.PutRequest) (library.Entry, error)
	Get(ctx context.Context, id int64) (library.Entry, error)
	Restore(ctx context.Context, id int64) (library.Entry, error)
	Purge(ctx context.Context, id int64) error
	// Find answers the newest entry that took the file at the original path, for a replace that stopped before it noted the entry.
	Find(ctx context.Context, originalPath string) (library.Entry, bool, error)
}

// Converted is a finished conversion, for the history, the statistics and the interface.
type Converted struct {
	MediaObjectID, CategoryID                         int64
	Kind, Engine, Path                                string
	OriginalBytes, ResultBytes, EncodeSeconds, Passes int64
	At                                                time.Time
}

// Reporter is told of every finished conversion; it never fails one.
type Reporter interface {
	Converted(ctx context.Context, converted Converted)
	// Reverted takes a restored conversion back out of the savings: the same figures, subtracted.
	Reverted(ctx context.Context, converted Converted)
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
	// Devices names the render device of a GPU engine.
	Devices  func(engine transcode.Accel) string
	Prober   Prober
	Tagger   Tagger
	Bin      Bin
	Reporter Reporter
	Starter  transcode.Starter
	Slots    *Slots
	Queue    Queue
	FS       fsx.FS
	Clock    clock.Clock
	Logger   *slog.Logger
	// ScratchDir is where ffmpeg's temporary files go.
	ScratchDir string
	// SafeMode makes the whole pipeline do nothing, whatever the master switch says.
	SafeMode bool

	mu sync.Mutex
	// blockedRoots are the roots found short of space, until when.
	blockedRoots map[int64]time.Time
	served       map[int64]int64
	reconciled   bool
	serveSeq     int64
}
