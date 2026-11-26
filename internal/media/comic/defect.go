package comic

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// pageArchives are the extensions whose files are nothing but a bundle of page images.
var pageArchives = map[string]bool{".cbz": true, ".cbr": true, ".cb7": true, ".cbt": true}

// defectOf says why a probed comic file is defective: its archive cannot be read, or it holds no page at all. "" is a good file.
func defectOf(ext string, facts Facts, probeErr error) (reason, detail string) {
	switch {
	case probeErr != nil:
		return library.DefectUnreadable, probeErr.Error()
	case pageArchives[strings.ToLower(ext)] && facts.Pages.Count == 0:
		return library.DefectEmpty, "no page images"
	}
	return "", ""
}

// markDefect records or clears the file's defect in the one table every media type shares (Files, Defective).
func markDefect(ctx context.Context, physical library.Physical, logger *slog.Logger, mediaObjectID, sizeBytes int64, ext string, facts Facts, probeErr error, at time.Time) {
	reason, detail := defectOf(ext, facts, probeErr)
	var err error
	if reason == "" {
		err = physical.ClearDefect(ctx, mediaObjectID)
	} else {
		err = physical.RecordDefect(ctx, mediaObjectID, sizeBytes, reason, detail, at)
	}
	if err != nil {
		logger.Warn("comic file defect not recorded", slog.Int64("media_object_id", mediaObjectID), slog.Any("error", err))
	}
}
