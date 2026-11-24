package transcoder

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/comicrepack"
	"github.com/makearr/Solarr/internal/platform/iogate"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// KindComic is the conversion of a comic archive to a cbz; its pages may be re-encoded and downscaled on the way.
const KindComic = "comic"

// engineComic is what a finished comic conversion records as its engine.
const engineComic transcode.Accel = "comic"

// pageSizeSlack is how far above the target's page edges a file's median page may be before it is downscaled, in percent.
const pageSizeSlack = 110

// minSavingPct is how much a repack that changes only the pages must save, else the file is left as it is.
const minSavingPct = 10

// Reasons that apply to comics only.
const (
	ReasonImageFormat          ReasonCode = "image_format"
	ReasonPageSize             ReasonCode = "page_size"
	ReasonNoPages              ReasonCode = "no_pages"
	ReasonSavesTooLittle       ReasonCode = "saves_too_little"
	ReasonSourceUnreadable     ReasonCode = "source_unreadable"
	ReasonUnsupportedContainer ReasonCode = "unsupported_container"
)

// ComicFacts are the facts of an archive's pages, read without extracting it.
type ComicFacts struct {
	// Container is the file extension without its dot, lower case.
	Container               string
	Pages                   int
	MedianLong, MedianShort int
	ImageFormat             string
}

// ComicProber reads a comic archive's page facts; the comic module's probe is the real one.
type ComicProber interface {
	ProbeComic(ctx context.Context, absolute string) (ComicFacts, error)
}

// AnalyzeComic decides what converting a comic file to the target's page aim would do.
func AnalyzeComic(facts ComicFacts, target settings.QualityTarget) Plan {
	switch facts.Container {
	case "cbz", "zip", "cbr", "cb7", "cbt":
	default:
		return Plan{Verdict: NotNeeded, Reasons: []Reason{{Code: ReasonUnsupportedContainer, Stream: -1, Have: facts.Container}}}
	}
	if facts.Pages == 0 {
		return Plan{Verdict: Blocked, Reasons: []Reason{{Code: ReasonNoPages, Stream: -1}}}
	}
	var reasons []Reason
	if facts.Container != "cbz" {
		reasons = append(reasons, Reason{Code: ReasonContainer, Stream: -1, Have: facts.Container, Want: "cbz"})
	}
	if want := comicrepack.CanonicalFormat(target.ImageFormat); (want == "webp" || want == "jpg") && facts.ImageFormat != "" && comicrepack.CanonicalFormat(facts.ImageFormat) != want {
		reasons = append(reasons, Reason{Code: ReasonImageFormat, Stream: -1, Have: comicrepack.CanonicalFormat(facts.ImageFormat), Want: want})
	}
	if over(facts.MedianLong, target.PageLong) || over(facts.MedianShort, target.PageShort) {
		reasons = append(reasons, Reason{Code: ReasonPageSize, Stream: -1,
			Have: pageEdges(int64(facts.MedianLong), int64(facts.MedianShort)), Want: pageEdges(target.PageLong, target.PageShort)})
	}
	if len(reasons) == 0 {
		return Plan{Verdict: NotNeeded, Reasons: []Reason{{Code: ReasonNothingToDo, Stream: -1}}}
	}
	return Plan{Verdict: Needed, Reasons: reasons, Container: "cbz"}
}

// over says a measured page edge is clearly above the aim; an edge nobody measured or aimed at never is.
func over(median int, aim int64) bool {
	return aim > 0 && median > 0 && int64(median)*100 > aim*pageSizeSlack
}

func pageEdges(long, short int64) string { return itoa(long) + "x" + itoa(short) }

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

// AimOf is what the plan asks of the pages: only what a reason of the plan names, so a repack never changes pages the plan found fine.
func AimOf(plan Plan, target settings.QualityTarget) comicrepack.Aim {
	var aim comicrepack.Aim
	for _, reason := range plan.Reasons {
		switch reason.Code {
		case ReasonImageFormat:
			aim.Format, aim.Quality = comicrepack.CanonicalFormat(target.ImageFormat), int(target.ImageQuality)
		case ReasonPageSize:
			aim.PageLong, aim.PageShort = int(target.PageLong), int(target.PageShort)
		}
	}
	return aim
}

// pagesChanged says the plan re-encodes or downscales pages; a container change alone may not shrink a file.
func pagesChanged(plan Plan) bool {
	for _, reason := range plan.Reasons {
		if reason.Code == ReasonImageFormat || reason.Code == ReasonPageSize {
			return true
		}
	}
	return false
}

// comicFacts reads the file on its disk's turn; the container is the extension the library keeps it under.
func (s *Service) comicFacts(ctx context.Context, absolute string) (ComicFacts, error) {
	var facts ComicFacts
	err := iogate.OnDisk(ctx, "", []string{absolute}, func() (probeErr error) {
		facts, probeErr = s.Comics.ProbeComic(ctx, absolute)
		return probeErr
	})
	if err != nil {
		return ComicFacts{}, err
	}
	facts.Container = strings.ToLower(strings.TrimPrefix(path.Ext(absolute), "."))
	return facts, nil
}

// comicPlan judges the file of a run; an archive that cannot be read is blocked, not failed again and again.
func (s *Service) comicPlan(ctx context.Context, run *fileRun) (Plan, error) {
	if s.Comics == nil {
		return Plan{}, errors.New("transcoder: no comic reader is wired")
	}
	facts, err := s.comicFacts(ctx, run.abs)
	if err != nil {
		if ctx.Err() != nil {
			return Plan{}, ctx.Err()
		}
		return Plan{Verdict: Blocked, Reasons: []Reason{{Code: ReasonSourceUnreadable, Stream: -1, Have: err.Error()}}}, nil
	}
	return AnalyzeComic(facts, run.target), nil
}

// judgeComic is the stored judgement of one library file; ok is false for a file that cannot be judged now.
func (s *Service) judgeComic(ctx context.Context, object library.ObjectToAnalyze, target settings.QualityTarget) (Judgement, bool, error) {
	root, known := s.Settings.RootFolder(object.RootFolderID)
	if !known || !root.Online || s.Comics == nil {
		return Judgement{}, false, nil
	}
	facts, err := s.comicFacts(ctx, path.Join(root.Path, object.RelPath))
	var plan Plan
	switch {
	case ctx.Err() != nil:
		return Judgement{}, false, ctx.Err()
	case err != nil:
		plan = Plan{Verdict: Blocked, Reasons: []Reason{{Code: ReasonSourceUnreadable, Stream: -1, Have: err.Error()}}}
	default:
		plan = AnalyzeComic(facts, target)
	}
	judgement, err := Judge(plan, transcode.Software)
	return judgement, err == nil, err
}

// convertComic repacks the file once; a result that is not worth having leaves the original alone.
func (s *Service) convertComic(ctx context.Context, run *fileRun) error {
	output := s.outputPath(run, 0)
	if err := s.FS.MkdirAll(path.Dir(output), 0o755); err != nil {
		return err
	}
	release, err := s.acquireLane(ctx, run.plan.Lane(transcode.Software), run.progress)
	if err != nil {
		return err
	}
	started := s.Clock.Now()
	repacked, err := comicrepack.Repack(ctx, s.FS, run.abs, output, output+".pages", AimOf(run.plan, run.target), func(done, total int) {
		run.progress.Set(int64(done), int64(total))
	})
	release()
	if err != nil {
		s.discard(output)
		return err
	}
	seconds := int64(s.Clock.Since(started).Seconds())
	info, err := s.FS.Stat(output)
	if err != nil || info.Size == 0 {
		s.discard(output)
		return s.refusedComic(ctx, run, ReasonUnreadable, "", "")
	}
	if repacked.Pages == 0 {
		s.discard(output)
		return s.refusedComic(ctx, run, ReasonStreamCount, "pages", "0")
	}
	if !WorthKeeping(run.plan, run.info.Size, info.Size) {
		s.discard(output)
		return s.settleComicSkipped(ctx, run, ReasonSavesTooLittle, itoa(info.Size*100/max(run.info.Size, 1)))
	}
	outcome := Outcome{Accepted: true, RatioPct: int(info.Size * 100 / max(run.info.Size, 1))}
	return s.finish(ctx, run, transcode.File{SizeBytes: info.Size}, outcome, engineComic, 0, seconds, output)
}

// WorthKeeping says the result earns replacing the original: a new container always does, a pages-only repack must shrink the file by minSavingPct.
func WorthKeeping(plan Plan, originalBytes, resultBytes int64) bool {
	return containerChanged(plan) || !pagesChanged(plan) || resultBytes*100 <= originalBytes*(100-minSavingPct)
}

func containerChanged(plan Plan) bool {
	for _, reason := range plan.Reasons {
		if reason.Code == ReasonContainer {
			return true
		}
	}
	return false
}

// settleComicSkipped records that converting would not pay, so the file is not tried again until its target changes.
func (s *Service) settleComicSkipped(ctx context.Context, run *fileRun, code ReasonCode, have string) error {
	reasons, _ := json.Marshal([]Reason{{Code: code, Stream: -1, Have: have}})
	if err := s.Items.SetJudgement(ctx, run.item.MediaObjectID, string(reasons), "", ""); err != nil {
		return err
	}
	return s.settle(ctx, run.item.MediaObjectID, library.TranscodeNotNeeded, func(item *library.TranscodeItem) { item.Interrupted, item.Lane = false, "" })
}

// refusedComic fails a result that is not a usable cbz; the ordinary attempt rules apply.
func (s *Service) refusedComic(ctx context.Context, run *fileRun, code ReasonCode, want, have string) error {
	failures := []Reason{{Code: code, Stream: -1, Want: want, Have: have}}
	return s.refused(ctx, run, Outcome{Failures: failures}, AfterRefused(itemStateOf(run.item), failures, PassSettings{}, false))
}
