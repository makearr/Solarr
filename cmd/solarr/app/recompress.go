package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// imageRecompress makes the stored pictures of library items and the video previews again at the sizes and quality now
// set, one at a time; a picture that would not come out smaller is left as it is.
type imageRecompress struct {
	fs                    fsx.FS
	coverDir, previewsDir string
	images                func() settings.ImageSettings
	logger                *slog.Logger
	mu                    sync.Mutex
	status                apiserver.RecompressStatus
}

func (r *imageRecompress) Status() apiserver.RecompressStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Start begins a run in the background and answers false when one is already going.
func (r *imageRecompress) Start(ctx context.Context) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Running {
		return false
	}
	now := clock.NewSystem().Now()
	r.status = apiserver.RecompressStatus{Running: true, StartedAt: &now}
	go r.run(ctx)
	return true
}

// recompressedMarker records, beside the pictures, the figures the last full run used: lossy pictures lose a little each
// time they are made again, so a run at the same figures only takes the pictures made since.
const recompressedMarker = ".recompressed"

func (r *imageRecompress) run(ctx context.Context) {
	images := r.images()
	figures := fmt.Sprintf("cover %d/%d preview %d/%d", images.CoverMaxEdge, images.CoverQuality, images.PreviewMaxEdge, images.PreviewQuality)
	var since time.Time
	if marker := filepath.Join(r.coverDir, recompressedMarker); r.read2(marker) == figures {
		if info, err := r.fs.Stat(marker); err == nil {
			since = info.ModTime
		}
	}
	started := clock.NewSystem().Now()
	files := r.listFiles(ctx, since)
	r.update(func(s *apiserver.RecompressStatus) { s.Total = int64(len(files)) })
	for _, file := range files {
		if ctx.Err() != nil {
			break
		}
		saved, err := r.recompress(file, images)
		r.update(func(s *apiserver.RecompressStatus) {
			s.Done++
			s.SavedBytes += saved
			if err != nil {
				s.Failed++
			}
		})
	}
	if ctx.Err() == nil {
		marker := filepath.Join(r.coverDir, recompressedMarker)
		_ = fsx.WriteAtomic(r.fs, marker, func(w io.Writer) error { _, err := io.WriteString(w, figures); return err })
		_ = r.fs.Chtimes(marker, started)
	}
	finished := clock.NewSystem().Now()
	r.update(func(s *apiserver.RecompressStatus) { s.Running, s.FinishedAt = false, &finished })
	r.logger.Info("pictures recompressed", slog.Int64("saved_bytes", r.Status().SavedBytes), slog.Int64("failed", r.Status().Failed))
}

func (r *imageRecompress) update(change func(*apiserver.RecompressStatus)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	change(&r.status)
}

// listFiles are the stored covers (.webp, one folder) and the previews (.jpg, a folder per thousand ids).
func (r *imageRecompress) listFiles(ctx context.Context, since time.Time) []string {
	var files []string
	if entries, err := r.fs.ReadDir(r.coverDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir && strings.HasSuffix(entry.Name, ".webp") && r.madeSince(filepath.Join(r.coverDir, entry.Name), since) {
				files = append(files, filepath.Join(r.coverDir, entry.Name))
			}
		}
	}
	folders, err := r.fs.ReadDir(r.previewsDir)
	if err != nil {
		return files
	}
	for _, folder := range folders {
		if !folder.IsDir || ctx.Err() != nil {
			continue
		}
		entries, err := r.fs.ReadDir(filepath.Join(r.previewsDir, folder.Name))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if path := filepath.Join(r.previewsDir, folder.Name, entry.Name); !entry.IsDir && strings.HasSuffix(entry.Name, ".jpg") && r.madeSince(path, since) {
				files = append(files, path)
			}
		}
	}
	return files
}

// recompress rewrites one picture when the result is smaller and answers the bytes saved.
func (r *imageRecompress) recompress(path string, images settings.ImageSettings) (int64, error) {
	info, err := r.fs.Stat(path)
	if err != nil {
		return 0, err
	}
	source, err := r.read(path)
	if err != nil {
		return 0, err
	}
	var rewritten []byte
	if strings.HasSuffix(path, ".webp") {
		rewritten, err = artwork.Reencode(source, coverEdge(path, int(images.CoverMaxEdge)))
	} else {
		rewritten, err = reencodePreview(source, int(images.PreviewMaxEdge), int(images.PreviewQuality))
	}
	if err != nil || int64(len(rewritten)) >= info.Size {
		return 0, err
	}
	if err := fsx.WriteAtomic(r.fs, path, func(w io.Writer) error { _, err := w.Write(rewritten); return err }); err != nil {
		return 0, err
	}
	return info.Size - int64(len(rewritten)), nil
}

// madeSince says a picture is newer than the last full run; without a last run every picture is.
func (r *imageRecompress) madeSince(path string, since time.Time) bool {
	if since.IsZero() {
		return true
	}
	info, err := r.fs.Stat(path)
	return err != nil || info.ModTime.After(since)
}

// read2 is the text of a small file, empty when it is not there.
func (r *imageRecompress) read2(path string) string {
	data, err := r.read(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func (r *imageRecompress) read(path string) ([]byte, error) {
	file, err := r.fs.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, 64<<20))
}

// coverEdge is the longest edge a stored cover is held to: Thumb and Small keep theirs, Full follows the setting.
func coverEdge(path string, full int) int {
	switch {
	case strings.HasSuffix(path, "-thumb.webp"):
		return int(artwork.Thumb)
	case strings.HasSuffix(path, "-small.webp"):
		return int(artwork.Small)
	}
	return full
}

func reencodePreview(source []byte, maxEdge, quality int) ([]byte, error) {
	img, err := artwork.Decode(source)
	if err != nil {
		return nil, err
	}
	return artwork.EncodeJPEGAt(artwork.ResizeLongestEdge(img, maxEdge), quality)
}
