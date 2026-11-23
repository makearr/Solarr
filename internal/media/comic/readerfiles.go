package comic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/comicinfo"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/sidecar"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ReaderFilesJobType is the registered type of the pass that writes what Kavita and Komga read.
const ReaderFilesJobType = jobs.Type("comic.reader_files")

// ReaderFilesDefinition declares the pass.
var ReaderFilesDefinition = works.ServerFilesDefinition("Writing Kavita and Komga files", ReaderFilesJobType)

// ReaderFiles writes series.json beside a series' files and ComicInfo.xml into its cbz files, as the library's Kavita and
// Komga setting asks: fill adds what is missing, replace sets what Solarr knows. A file is rewritten only when the result
// differs, through a copy swapped in whole, and never on a root that is read only, offline or not writable.
type ReaderFiles struct {
	FS     fsx.FS
	Reader library.SeriesReader
	Roots  interface {
		RootFolder(id int64) (settings.RootFolder, bool)
	}
	// OptionOf answers one of the library's own settings; KindOf the library's kind.
	OptionOf func(categoryID int64, key, fallback string) string
	KindOf   func(categoryID int64) string
	// RootsOf answers the roots of a library, which a preview walks.
	RootsOf func(categoryID int64) []settings.RootFolder
	// Rehash records the size and quick hash a rewritten file has now.
	Rehash func(ctx context.Context, mediaObjectID int64, filePath string) error
	Clock  clock.Clock
	Logger *slog.Logger
}

// ReaderFilesEnabled says a library asks for reader files.
func ReaderFilesEnabled(category settings.Category) bool {
	mode := category.FileManagement.ModuleOption(string(media.FileReaderFiles), ReaderFilesOff)
	return mode == ReaderFilesFill || mode == ReaderFilesReplace
}

// WriteSeries writes the series' reader files into each writable root its files are on, answering how many files it wrote.
func (w ReaderFiles) WriteSeries(ctx context.Context, categoryID, workID int64) (int, error) {
	return w.series(ctx, categoryID, workID, w.OptionOf(categoryID, string(media.FileReaderFiles), ReaderFilesOff), false)
}

// Preview counts the files a pass in this mode would write across the library, writing none.
func (w ReaderFiles) Preview(ctx context.Context, categoryID int64, mode string) (int, error) {
	total := 0
	for _, root := range w.RootsOf(categoryID) {
		var afterID int64
		for {
			ids, err := w.Reader.WorksOnRoot(ctx, MediaType, root.ID, afterID, 200)
			if err != nil {
				return total, err
			}
			for _, workID := range ids {
				afterID = workID
				count, err := w.series(ctx, categoryID, workID, mode, true)
				if err != nil {
					return total, err
				}
				total += count
			}
			if len(ids) < 200 {
				break
			}
		}
	}
	return total, nil
}

// series is WriteSeries in a given mode; dry counts what would be written and touches nothing.
func (w ReaderFiles) series(ctx context.Context, categoryID, workID int64, mode string, dry bool) (int, error) {
	if mode != ReaderFilesFill && mode != ReaderFilesReplace {
		return 0, nil
	}
	detail, err := w.Reader.GetOf(ctx, MediaType, categoryID, workID, w.Clock.Now())
	if err != nil {
		return 0, err
	}
	paths, err := w.Reader.PartPaths(ctx, workID, "")
	if err != nil {
		return 0, err
	}
	manga := isMangaKind(w.KindOf(categoryID))
	written := 0
	folders := map[string]bool{}
	for _, file := range paths {
		root, found := w.Roots.RootFolder(file.RootFolderID)
		if !found || !sidecar.WritableRoot(root) {
			continue
		}
		folders[path.Join(root.Path, path.Dir(file.RelPath))] = true
		if !strings.EqualFold(path.Ext(file.RelPath), ".cbz") {
			continue
		}
		changed, err := w.rewrite(ctx, path.Join(root.Path, file.RelPath), file, issueInfo(detail, file, manga), mode, dry)
		if err != nil {
			w.Logger.Warn("comicinfo not written", slog.Int64("work_id", workID), slog.Any("error", err))
			continue
		}
		if changed {
			written++
			if dry {
				continue
			}
			if err := w.Rehash(ctx, file.MediaObjectID, path.Join(root.Path, file.RelPath)); err != nil {
				w.Logger.Warn("rewritten cbz not measured, the next scan will", slog.Int64("media_object_id", file.MediaObjectID), slog.Any("error", err))
			}
		}
	}
	body, err := comicinfo.SeriesJSON(comicinfo.Series{Name: detail.Summary.Title, Publisher: detail.Summary.Fields["publisher"], Year: int(detail.Summary.Year),
		Description: detail.Summary.Description, Type: detail.Summary.Fields[FieldFormat], ComicVineID: detail.IDs[ProviderComicVine]})
	if err != nil {
		return written, err
	}
	for _, folder := range sortedFolders(folders) {
		target := path.Join(folder, "series.json")
		if dry {
			current, err := w.read(target)
			if errors.Is(err, fs.ErrNotExist) || (err == nil && mode == ReaderFilesReplace && !bytes.Equal(current, body)) {
				written++
			}
			continue
		}
		if mode == ReaderFilesReplace {
			if replaced, err := w.replaceFile(target, body); err != nil {
				return written, err
			} else if replaced {
				written++
			}
			continue
		}
		made, err := sidecar.Writer{FS: w.FS}.Once(target, func() ([]byte, error) { return body, nil })
		if err != nil {
			return written, err
		}
		written += made
	}
	return written, nil
}

func sortedFolders(folders map[string]bool) []string {
	keys := make([]string, 0, len(folders))
	for key := range folders {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (w ReaderFiles) replaceFile(target string, body []byte) (bool, error) {
	if current, err := w.read(target); err == nil && bytes.Equal(current, body) {
		return false, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return true, fsx.WriteAtomic(w.FS, target, func(out io.Writer) error { _, err := out.Write(body); return err })
}

func (w ReaderFiles) read(target string) ([]byte, error) {
	file, err := w.FS.Open(target)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, 1<<20))
}

// issueInfo is what Solarr knows of one issue, in ComicInfo's terms.
func issueInfo(detail library.SeriesDetailRow, file library.PartPath, manga bool) comicinfo.Info {
	kind, number, _ := strings.Cut(file.MediaRef, ":")
	info := comicinfo.Info{
		Series: detail.Summary.Title, Number: number, Title: file.Title, Publisher: detail.Summary.Fields["publisher"], Summary: "",
		Genre: strings.Join(detail.Summary.Genres, ", "), Credits: map[string][]string{},
		Notes: "Solarr " + idNotes(detail.IDs),
	}
	switch {
	case kind == PartVolume:
		info.Number = ""
		info.Volume, _ = strconv.Atoi(number)
	case detail.Summary.Year > 0:
		info.Volume = int(detail.Summary.Year)
	}
	if len(file.Date) >= 4 {
		info.Year, _ = strconv.Atoi(file.Date[:4])
		if len(file.Date) >= 7 {
			info.Month, _ = strconv.Atoi(file.Date[5:7])
		}
		if len(file.Date) >= 10 {
			info.Day, _ = strconv.Atoi(file.Date[8:10])
		}
	}
	if manga {
		info.Manga = "YesAndRightToLeft"
	}
	for _, credit := range detail.Credits {
		if credit.PartID == 0 {
			info.Credits[credit.Role] = append(info.Credits[credit.Role], credit.Name)
		}
	}
	return info
}

func idNotes(ids map[string]string) string {
	keys := make([]string, 0, len(ids))
	for key := range ids {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var out []string
	for _, key := range keys {
		out = append(out, key+":"+ids[key])
	}
	return strings.Join(out, " ")
}

// rewrite puts ComicInfo.xml into the cbz when it would differ, swapping in a whole new file; answers whether it did.
func (w ReaderFiles) rewrite(ctx context.Context, target string, _ library.PartPath, info comicinfo.Info, mode string, dry bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	stat, err := w.FS.Stat(target)
	if err != nil {
		return false, err
	}
	source, err := w.FS.Open(target)
	if err != nil {
		return false, err
	}
	defer func() { _ = source.Close() }()
	existing, err := comicinfo.ReadFromCBZ(source, stat.Size)
	if err != nil {
		return false, err
	}
	data, changed, err := comicinfo.Merge(existing, info, mode)
	if err != nil || !changed || dry {
		return changed && dry, err
	}
	err = fsx.WriteAtomic(w.FS, target, func(out io.Writer) error { return comicinfo.RewriteCBZ(source, stat.Size, out, data) })
	if err != nil {
		return false, fmt.Errorf("rewrite %s: %w", target, err)
	}
	return true, nil
}

// ReaderFilesJob writes the reader files of every series in each comic library that asks for them.
type ReaderFilesJob struct {
	Writer ReaderFiles
	// Libraries answers every library, with the roots each is on.
	Libraries func() []settings.Category
	RootsOf   func(categoryID int64) []settings.RootFolder
	Logger    *slog.Logger
}

// Run implements jobs.Handler.
func (j ReaderFilesJob) Run(ctx context.Context, job jobs.Job) error {
	return works.ServerFilesPass{
		MediaType: MediaType, Noun: "series", Reader: j.Writer.Reader, Write: j.Writer.WriteSeries, Enabled: ReaderFilesEnabled,
		Libraries: j.Libraries, RootsOf: j.RootsOf, Logger: j.Logger,
	}.Run(ctx, job)
}

// EnqueueReaderFiles submits one pass over every comic series.
func EnqueueReaderFiles(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return works.EnqueueServerFiles(ctx, queue, ReaderFilesJobType)
}

// EnqueueReaderFilesFor submits a pass for one series, as right after an import.
func EnqueueReaderFilesFor(ctx context.Context, queue *jobs.Queue, categoryID, workID int64) (jobs.EnqueueResult, error) {
	return works.EnqueueServerFilesFor(ctx, queue, ReaderFilesJobType, categoryID, workID)
}
