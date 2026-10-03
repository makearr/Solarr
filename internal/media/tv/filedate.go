package tv

import (
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// airInstant is the moment an episode aired, from its date: midnight UTC, the most a date alone says.
func airInstant(date string) (time.Time, bool) {
	at, err := time.Parse("2006-01-02", date)
	return at, err == nil
}

// changeFileDate is a library's "Change File Date" for a placed file: its modification date becomes the air date of
// the first episode it holds. Never on a root Solarr may not write to (Invariant 69); a failure is only logged.
func (i Importer) changeFileDate(root settings.RootFolder, fileAbs string, fileManagement settings.FileManagement, parts []library.ResolvedPart) {
	if fileManagement.ChangeFileDate != settings.FileDateReleaseDate || root.ReadOnly || !root.Writable {
		return
	}
	for _, part := range parts {
		if at, ok := airInstant(part.Date); ok {
			if err := i.config.FS.Chtimes(fileAbs, at); err != nil {
				i.logger.Warn("tv import: file date not changed", slog.String("path", fileAbs), slog.Any("error", err))
			}
			return
		}
	}
}
