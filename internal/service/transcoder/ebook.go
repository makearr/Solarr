package transcoder

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/ebookconv"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// KindEbook is the conversion of an ebook to the format its target names (epub).
const KindEbook = "ebook"

// engineEbook is what a finished ebook conversion records as its engine.
const engineEbook transcode.Accel = "ebook"

// goReadable are the formats the pure-Go writer turns into epub.
var goReadable = map[string]bool{"txt": true, "html": true, "htm": true}

// AnalyzeEbook judges one ebook file by its name against the target's format: a file already in it is fine, one pure Go
// or pandoc can turn into it is needed, any other is not converted.
func AnalyzeEbook(name string, target settings.QualityTarget) Plan {
	have := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	want := strings.ToLower(target.Container)
	switch {
	case want != "epub":
		return Plan{Verdict: NotNeeded, Reasons: []Reason{{Code: ReasonUnsupportedContainer, Stream: -1, Have: want}}}
	case have == want:
		return Plan{Verdict: NotNeeded, Reasons: []Reason{{Code: ReasonNothingToDo, Stream: -1}}}
	}
	return Plan{Verdict: Needed, Reasons: []Reason{{Code: ReasonContainer, Stream: -1, Have: have, Want: want}}, Container: want, Fixed: LaneEbook}
}

// ebookPlan is the plan of a run: the file's own name against the target.
func (s *Service) ebookPlan(run *fileRun) Plan { return AnalyzeEbook(run.abs, run.target) }

// convertEbook writes the epub of the file once; a source nothing here reads is refused like an unusable result.
func (s *Service) convertEbook(ctx context.Context, run *fileRun) error {
	output := s.outputPath(run, 0)
	if err := s.FS.MkdirAll(path.Dir(output), 0o755); err != nil {
		return err
	}
	release, err := s.acquireLane(ctx, LaneEbook, run.progress)
	if err != nil {
		return err
	}
	started := s.Clock.Now()
	err = s.writeEPUB(ctx, run.abs, output)
	release()
	if errors.Is(err, ebookconv.ErrNeedsPandoc) {
		s.discard(output)
		return s.refusedComic(ctx, run, ReasonUnsupportedContainer, "epub", strings.TrimPrefix(path.Ext(run.abs), "."))
	}
	if err != nil {
		s.discard(output)
		return err
	}
	info, err := s.FS.Stat(output)
	if err != nil || info.Size == 0 {
		s.discard(output)
		return s.refusedComic(ctx, run, ReasonUnreadable, "", "")
	}
	outcome := Outcome{Accepted: true, RatioPct: int(info.Size * 100 / max(run.info.Size, 1))}
	return s.finish(ctx, run, transcode.File{SizeBytes: info.Size}, outcome, engineEbook, 0, int64(s.Clock.Since(started).Seconds()), output)
}

// writeEPUB converts the source in pure Go where it can, and through pandoc where that exists.
func (s *Service) writeEPUB(ctx context.Context, source, output string) error {
	extension := strings.ToLower(strings.TrimPrefix(path.Ext(source), "."))
	if !goReadable[extension] {
		return ebookconv.ViaPandoc(ctx, s.Ebooks, source, output)
	}
	in, err := s.FS.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return fsx.WriteAtomic(s.FS, output, func(w io.Writer) error {
		title := strings.TrimSuffix(path.Base(source), path.Ext(source))
		if extension == "txt" {
			return ebookconv.TextToEPUB(title, in, w)
		}
		return ebookconv.HTMLToEPUB(title, in, w)
	})
}
