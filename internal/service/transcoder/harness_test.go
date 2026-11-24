package transcoder_test

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/transcode"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

// memItems is the judged files in memory, with the transitions the real table allows.
type memItems struct {
	mu       sync.Mutex
	objects  []library.ObjectToAnalyze
	facts    map[int64]library.ObjectFacts
	judged   []library.JudgedTranscodeItem
	items    map[int64]*library.TranscodeItem
	paths    map[int64][]library.ObjectPath
	health   map[string]library.TranscodeHealth
	replaced []library.ReplacedFile
	// stages is every journal stage written, in order.
	stages []string
}

func newMemItems() *memItems {
	return &memItems{facts: map[int64]library.ObjectFacts{}, items: map[int64]*library.TranscodeItem{}, paths: map[int64][]library.ObjectPath{}, health: map[string]library.TranscodeHealth{}}
}

func (m *memItems) Judge(_ context.Context, judged []library.JudgedTranscodeItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.judged = append(m.judged, judged...)
	for _, one := range judged {
		if existing := m.items[one.MediaObjectID]; existing != nil && existing.State == library.TranscodeRunning {
			continue
		}
		m.items[one.MediaObjectID] = &library.TranscodeItem{
			MediaObjectID: one.MediaObjectID, CategoryID: one.CategoryID, Kind: one.Kind, State: one.State, Lane: one.Lane, TargetID: one.TargetID,
			SetupRevision: one.SetupRevision, ReasonsJSON: one.ReasonsJSON, PlanJSON: one.PlanJSON, WarningsJSON: one.WarningsJSON,
		}
	}
	return nil
}

func (m *memItems) ObjectsToAnalyze(_ context.Context, _ []int64, _, _, afterID, pageSize int64) ([]library.ObjectToAnalyze, error) {
	var page []library.ObjectToAnalyze
	for _, object := range m.objects {
		if object.MediaObjectID > afterID && int64(len(page)) < pageSize {
			page = append(page, object)
		}
	}
	return page, nil
}

func (m *memItems) Facts(_ context.Context, ids []int64) (map[int64]library.ObjectFacts, error) {
	out := map[int64]library.ObjectFacts{}
	for _, id := range ids {
		out[id] = m.facts[id]
	}
	return out, nil
}

func (m *memItems) Get(_ context.Context, id int64) (library.TranscodeItem, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item := m.items[id]; item != nil {
		return *item, true, nil
	}
	return library.TranscodeItem{}, false, nil
}

func (m *memItems) Transition(_ context.Context, id int64, from, to library.TranscodeState, mutate func(*library.TranscodeItem)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil || item.State != from {
		return library.ErrTranscodeState
	}
	if mutate != nil {
		mutate(item)
	}
	item.State = to
	return nil
}

func (m *memItems) SetJudgement(_ context.Context, id int64, reasons, plan, warnings string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	item.ReasonsJSON, item.PlanJSON, item.WarningsJSON = reasons, plan, warnings
	return nil
}

func (m *memItems) SetJournal(_ context.Context, id int64, stage, workPath string, recycleID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	m.stages = append(m.stages, stage)
	item.ReplaceStage, item.WorkPath, item.OriginalRecycleID = stage, workPath, recycleID
	return nil
}

func (m *memItems) Paths(_ context.Context, id int64) ([]library.ObjectPath, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]library.ObjectPath(nil), m.paths[id]...), nil
}

func (m *memItems) ReplaceBytes(_ context.Context, replaced library.ReplacedFile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[replaced.MediaObjectID]
	if item == nil || item.State != library.TranscodeRunning {
		return library.ErrTranscodeState
	}
	m.replaced = append(m.replaced, replaced)
	m.paths[replaced.MediaObjectID] = []library.ObjectPath{{RootFolderID: replaced.RootFolderID, RelPath: replaced.NewRelPath}}
	item.State, item.OriginalRecycleID, item.ResultSizeBytes, item.Engine = library.TranscodeDone, replaced.Result.OriginalRecycleID, replaced.Result.ResultSizeBytes, replaced.Result.Engine
	return nil
}

func (m *memItems) pickable(categoryID int64, kind string) []library.TranscodePick {
	var picks []library.TranscodePick
	for _, item := range m.items {
		if item.State == library.TranscodeNeeded && item.CategoryID == categoryID && item.Kind == kind {
			root, rel := int64(0), ""
			if paths := m.paths[item.MediaObjectID]; len(paths) > 0 {
				root, rel = paths[0].RootFolderID, paths[0].RelPath
			}
			picks = append(picks, library.TranscodePick{MediaObjectID: item.MediaObjectID, Lane: item.Lane, LastRunSeq: item.LastRunSeq, RootFolderID: root, Interrupted: item.Interrupted, RelPath: rel})
		}
	}
	sort.Slice(picks, func(i, j int) bool {
		if picks[i].Interrupted != picks[j].Interrupted {
			return picks[i].Interrupted
		}
		return picks[i].RelPath < picks[j].RelPath
	})
	return picks
}

func (m *memItems) Pick(_ context.Context, _ library.TranscodeOrder, categoryID int64, kind string, limit int64) ([]library.TranscodePick, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return head(m.pickable(categoryID, kind), limit), nil
}

func (m *memItems) PickFarAway(_ context.Context, categoryID int64, kind string, _ int64, avoidPrefix string, limit int64) ([]library.TranscodePick, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []library.TranscodePick
	picks := m.pickable(categoryID, kind)
	for i := len(picks) - 1; i >= 0; i-- {
		if avoidPrefix == "" || !strings.HasPrefix(picks[i].RelPath, avoidPrefix) {
			out = append(out, picks[i])
		}
	}
	return head(out, limit), nil
}

func head(picks []library.TranscodePick, limit int64) []library.TranscodePick {
	if int64(len(picks)) > limit {
		return picks[:limit]
	}
	return picks
}

func (m *memItems) withState(state library.TranscodeState, journalled bool) []library.TranscodeItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []library.TranscodeItem
	for _, item := range m.items {
		if (!journalled && item.State == state) || (journalled && item.ReplaceStage != "") {
			out = append(out, *item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MediaObjectID < out[j].MediaObjectID })
	return out
}

func (m *memItems) Running(context.Context) ([]library.TranscodeItem, error) {
	return m.withState(library.TranscodeRunning, false), nil
}

func (m *memItems) Journalled(context.Context) ([]library.TranscodeItem, error) {
	return m.withState("", true), nil
}

func (m *memItems) Health(_ context.Context, kind string) (library.TranscodeHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stored, found := m.health[kind]; found {
		return stored, nil
	}
	return library.TranscodeHealth{Kind: kind, Mode: "ok"}, nil
}

func (m *memItems) SaveHealth(_ context.Context, health library.TranscodeHealth) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.health[health.Kind] = health
	return nil
}

// memSettings is one enabled library over one root.
type memSettings struct {
	global  settings.TranscodeGlobal
	target  settings.QualityTarget
	enabled bool
	setup   func(settings.TranscodeSetup) settings.TranscodeSetup
}

func (f *memSettings) Global() settings.TranscodeGlobal { return f.global }
func (f *memSettings) Libraries(context.Context) ([]settings.TranscodeLibrary, error) {
	target := f.target
	return []settings.TranscodeLibrary{{CategoryTranscode: settings.CategoryTranscode{CategoryID: 3, Enabled: f.enabled, Priority: 1}, Target: &target}}, nil
}

func (f *memSettings) Setup(context.Context, int64) (settings.StoredTranscodeSetup, error) {
	setup := settings.DefaultTranscodeSetup(f.target)
	if f.setup != nil {
		setup = f.setup(setup)
	}
	return settings.StoredTranscodeSetup{Setup: setup, Revision: 4}, nil
}

var memRoot = settings.RootFolder{ID: 7, Path: "/lib", Online: true, Writable: true}

func (f *memSettings) RootFolders(int64) []settings.RootFolder { return []settings.RootFolder{memRoot} }
func (f *memSettings) Roots() []settings.RootFolder            { return []settings.RootFolder{memRoot} }
func (f *memSettings) RootFolder(id int64) (settings.RootFolder, bool) {
	return memRoot, id == memRoot.ID
}
func (f *memSettings) Languages(int64) []string      { return nil }
func (f *memSettings) Retention(int64) time.Duration { return 24 * time.Hour }

type fakeHW map[transcode.Accel]transcode.EncoderSupport

func (f fakeHW) EncoderFor(engine transcode.Accel, _ transcode.Codec) (transcode.EncoderSupport, bool) {
	row, found := f[engine]
	row.Encoder = map[transcode.Accel]string{transcode.Software: "libx265", transcode.QSV: "hevc_qsv"}[engine]
	return row, found
}

func (f fakeHW) BestEngine(_ transcode.Codec, tenBit bool) (transcode.Accel, bool) {
	for _, engine := range []transcode.Accel{transcode.NVENC, transcode.QSV, transcode.VAAPI} {
		if row, found := f[engine]; found && row.Working && (!tenBit || row.TenBit) {
			return engine, true
		}
	}
	return "", false
}

// memBin moves files into a bin folder of the same filesystem and back.
type memBin struct {
	fs      fsx.FS
	mu      sync.Mutex
	entries map[int64]library.Entry
}

func (b *memBin) Put(_ context.Context, request library.PutRequest) (library.Entry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = map[int64]library.Entry{}
	}
	info, err := b.fs.Lstat(request.OriginalPath)
	if err != nil {
		return library.Entry{}, err
	}
	id := int64(len(b.entries) + 1)
	recycled := path.Join(request.RootPath, library.RecycleBinDirectory, "day", fmt.Sprintf("%d-%s", id, path.Base(request.OriginalPath)))
	if err := b.fs.MkdirAll(path.Dir(recycled), 0o755); err != nil {
		return library.Entry{}, err
	}
	if err := b.fs.Rename(request.OriginalPath, recycled); err != nil {
		return library.Entry{}, err
	}
	entry := library.Entry{ID: id, OriginalPath: request.OriginalPath, RecycledPath: recycled, Reason: request.Reason, SizeBytes: info.Size}
	b.entries[id] = entry
	return entry, nil
}

func (b *memBin) Get(_ context.Context, id int64) (library.Entry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.entries[id], nil
}

func (b *memBin) Find(_ context.Context, originalPath string) (library.Entry, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := int64(len(b.entries)); id > 0; id-- {
		if b.entries[id].OriginalPath == originalPath {
			return b.entries[id], true, nil
		}
	}
	return library.Entry{}, false, nil
}

func (b *memBin) Restore(_ context.Context, id int64) (library.Entry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.entries[id]
	return entry, b.fs.Rename(entry.RecycledPath, entry.OriginalPath)
}

// fakeProber answers the streams of a path from a table; a path without an entry cannot be read.
type fakeProber struct {
	mu    sync.Mutex
	files map[string]transcode.File
}

func (p *fakeProber) ProbeStreams(_ context.Context, file string) (transcode.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if found, ok := p.files[file]; ok {
		return found, nil
	}
	return transcode.File{}, fmt.Errorf("probe %s: unreadable", file)
}

func (p *fakeProber) set(file string, found transcode.File) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[file] = found
}

type recordedConversions struct {
	mu   sync.Mutex
	done []transcoder.Converted
}

func (r *recordedConversions) Converted(_ context.Context, converted transcoder.Converted) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done = append(r.done, converted)
}

type queuedJobs struct {
	mu       sync.Mutex
	requests []jobs.Request
}

func (q *queuedJobs) Enqueue(_ context.Context, request jobs.Request) (jobs.EnqueueResult, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.requests = append(q.requests, request)
	return jobs.EnqueueResult{}, nil
}

// pipeline is a whole conversion service over fakes.
type pipeline struct {
	svc      *transcoder.Service
	items    *memItems
	settings *memSettings
	fs       *fsx.Mem
	clock    *clock.Fake
	starter  *transcode.FakeStarter
	prober   *fakeProber
	bin      *memBin
	reported *recordedConversions
	queue    *queuedJobs
}

func newPipeline(t *testing.T, target settings.QualityTarget, hardware fakeHW) *pipeline {
	t.Helper()
	cl := clock.NewFakeAtEpoch()
	mem := fsx.NewMem(cl)
	target.ID = 9
	p := &pipeline{
		items: newMemItems(), fs: mem, clock: cl, starter: &transcode.FakeStarter{}, prober: &fakeProber{files: map[string]transcode.File{}},
		bin: &memBin{fs: mem}, reported: &recordedConversions{}, queue: &queuedJobs{},
		settings: &memSettings{global: settings.TranscodeGlobal{Enabled: true, Parallel: settings.LaneLimits{CPU: 1, GPU: 2, Audio: 4}}, target: target, enabled: true},
	}
	if hardware == nil {
		hardware = fakeHW{transcode.Software: {Working: true, TenBit: true}}
	}
	p.svc = &transcoder.Service{
		Items: p.items, Settings: p.settings, Hardware: hardware, Prober: p.prober, Bin: p.bin, Reporter: p.reported, Starter: p.starter,
		Slots: transcoder.NewSlots(func(bool) map[transcoder.Lane]int {
			return map[transcoder.Lane]int{transcoder.LaneCPU: 1, transcoder.LaneGPU: 2, transcoder.LaneAudio: 4}
		}, func() int { return 0 }, cl),
		Queue: p.queue, FS: mem, Clock: cl, Logger: discardLogger(),
	}
	return p
}

// writeFile puts a file of the given size at the path.
func (p *pipeline) writeFile(t *testing.T, file string, size int) {
	t.Helper()
	if err := p.fs.MkdirAll(path.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := p.fs.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(out, zeros{}, int64(size)); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

type zeros struct{}

func (zeros) Read(buffer []byte) (int, error) { return len(buffer), nil }

// add judges a file as needed and leaves it running, as the dispatcher would have.
func (p *pipeline) add(t *testing.T, id int64, rel string, kind string, source transcode.File, size int) {
	t.Helper()
	absolute := path.Join("/lib", rel)
	p.writeFile(t, absolute, size)
	source.SizeBytes = int64(size)
	p.prober.set(absolute, source)
	p.items.items[id] = &library.TranscodeItem{MediaObjectID: id, CategoryID: 3, Kind: kind, State: library.TranscodeRunning, TargetID: 9}
	p.items.paths[id] = []library.ObjectPath{{RootFolderID: 7, RelPath: rel}}
}

// outputs scripts what the fake ffmpeg leaves behind: a file of the size at the last argument, probed as result.
func (p *pipeline) outputs(t *testing.T, sizes []int, result func(size int) transcode.File) {
	t.Helper()
	call := 0
	p.starter.Next = func(args []string) transcode.FakeRun {
		out := args[len(args)-1]
		size := sizes[min(call, len(sizes)-1)]
		call++
		p.writeFile(t, out, size)
		p.prober.set(out, result(size))
		return transcode.FakeRun{Reports: []transcode.Progress{{OutTime: 100 * time.Second}}}
	}
}

func (p *pipeline) run(t *testing.T, id int64) error {
	t.Helper()
	return p.svc.RunFile(t.Context(), id, nil)
}

func (p *pipeline) item(id int64) library.TranscodeItem {
	item, _, _ := p.items.Get(context.Background(), id)
	return item
}

func (p *pipeline) exists(file string) bool {
	_, err := p.fs.Lstat(file)
	return err == nil
}
