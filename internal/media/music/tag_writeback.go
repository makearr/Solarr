package music

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// TagWritebackJobType is the tag writer: ids, metadata and covers into library files (revision D3).
const TagWritebackJobType = jobs.Type("music.tag_writeback")

// TagWritebackActionKey deduplicates the sweep: one queued run covers every root.
const TagWritebackActionKey = "music.tag_writeback"

// TagWritebackDefinition runs in maintenance: it rewrites files, and nothing waits on it.
var TagWritebackDefinition = jobs.Definition{
	Name: "Writing tags into files",
	Type: TagWritebackJobType, Pool: jobs.PoolFiles, Mutating: true, MaxAttempts: 1,
}

// tagWritebackPage is one read of candidates and one helper batch; tagWritebackPerRun paces the
// files written in one run, as the artwork sweep is paced. Files already current cost no write.
const (
	tagWritebackPage   = 200
	tagWritebackPerRun = 2000
)

// The keys written, in Picard's names; mediatool's helper maps each per format.
const (
	tagReleaseGroupID = "musicbrainz_releasegroupid"
	tagReleaseID      = "musicbrainz_albumid"
	tagRecordingID    = "musicbrainz_trackid"
	tagAlbumArtistID  = "musicbrainz_albumartistid"
	tagArtistID       = "musicbrainz_artistid"
	tagAlbum          = "album"
	tagAlbumArtist    = "albumartist"
	tagTitle          = "title"
	tagTrackNumber    = "tracknumber"
	tagDiscNumber     = "discnumber"
	tagDate           = "date"
	tagGenre          = "genre"
	tagCover          = "cover"
)

// TagOptions are one library's tagging choices (settings.Tagging, beside naming).
type TagOptions struct {
	WriteIDs, ReplaceIDs, WriteMetadata, EmbedCover bool
	// Navidrome is its consumer profile (D10): track-artist tags and id, and a cover.jpg beside the tracks.
	Navidrome bool
}

func (o TagOptions) any() bool { return o.WriteIDs || o.WriteMetadata || o.EmbedCover || o.Navidrome }

// TagRoot is one root folder the writer may touch: online, writable, not read-only, in a music
// library, with that library's options. Download folders are never roots.
type TagRoot struct {
	ID      int64
	Path    string
	Options TagOptions
}

// TagWritebackConfig is what the job needs; every field is required.
type TagWritebackConfig struct {
	Files library.TagWriteback
	Roots func(ctx context.Context) ([]TagRoot, error)
	Tool  mediatool.Tool
	FS    fsx.FS
	// ImagesDir holds the fetched covers (WebP); CoversDir gets the JPEG copies embedding needs.
	ImagesDir string
	CoversDir string
	Clock     clock.Clock
	Logger    *slog.Logger
}

// TagWritebackJob writes what a library's options ask into its identified files.
type TagWritebackJob struct{ config TagWritebackConfig }

// NewTagWritebackJob builds the handler.
func NewTagWritebackJob(config TagWritebackConfig) *TagWritebackJob {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &TagWritebackJob{config: config}
}

var tagProviders = library.TagProviders{
	Work: ProviderReleaseGroup, Edition: ProviderRelease, Part: ProviderRecording, Person: ProviderArtist,
	Genre: "musicbrainz",
}

// tagRun is one run's state: the roots, the covers converted so far, and the counts.
type tagRun struct {
	roots                    map[int64]TagRoot
	covers                   map[string]string
	folderCovers             map[string]bool
	written, current, failed int
	// progress counts files sent to the helper, for the status bar.
	progress *jobs.Progress
}

// Run writes up to tagWritebackPerRun files, library by library; the next run carries on.
func (j *TagWritebackJob) Run(ctx context.Context, job jobs.Job) error {
	roots, err := j.config.Roots(ctx)
	if err != nil {
		return err
	}
	run := &tagRun{roots: map[int64]TagRoot{}, covers: map[string]string{}, folderCovers: map[string]bool{}, progress: job.Progress}
	var ids []int64
	for _, root := range roots {
		if root.Options.any() {
			run.roots[root.ID] = root
			ids = append(ids, root.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	defer j.removeCovers(run)

	var afterID int64
	for run.written+run.failed < tagWritebackPerRun {
		candidates, err := j.config.Files.Candidates(ctx, tagProviders, ids, afterID, tagWritebackPage)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			break
		}
		if err := j.writePage(ctx, run, candidates); err != nil {
			return err
		}
		afterID = candidates[len(candidates)-1].MediaObjectID
	}
	if run.written+run.failed > 0 {
		j.config.Logger.Info("tag writer finished",
			slog.Int("written", run.written), slog.Int("already_current", run.current), slog.Int("failed", run.failed))
	}
	return nil
}

// writePage sends the helper every file whose wanted tags changed since it was last written.
func (j *TagWritebackJob) writePage(ctx context.Context, run *tagRun, candidates []library.TagCandidate) error {
	var edits []mediatool.TagEdit
	var digests []string
	var editOf []library.TagCandidate
	for _, candidate := range candidates {
		root := run.roots[candidate.RootFolderID]
		if root.Options.Navidrome && candidate.CoverKey != "" {
			j.folderCover(ctx, run, path.Dir(path.Join(root.Path, candidate.RelPath)), candidate.CoverKey)
		}
		edit, digest := tagEditFor(candidate, root.Options)
		if digest == candidate.Digest || (len(edit.Fill) == 0 && len(edit.Set) == 0 && edit.Cover == "") {
			continue
		}
		if edit.Cover != "" {
			if edit.Cover = j.coverJPEG(ctx, run, candidate.CoverKey); edit.Cover == "" {
				// Recorded without the cover, so a later run tries the picture again.
				withoutCover := root.Options
				withoutCover.EmbedCover = false
				edit, digest = tagEditFor(candidate, withoutCover)
			}
		}
		edit.Path = path.Join(root.Path, candidate.RelPath)
		edits = append(edits, edit)
		digests = append(digests, digest)
		editOf = append(editOf, candidate)
	}
	if len(edits) == 0 {
		return nil
	}

	results, err := j.config.Tool.WriteTags(ctx, edits)
	if err != nil {
		return err
	}
	run.progress.Add(int64(len(results)))
	now := j.config.Clock.Now().Unix()
	for k, result := range results {
		failure := ""
		switch {
		case result.Err != nil:
			failure = result.Err.Error()
			run.failed++
		case len(result.Changed) == 0:
			run.current++
		default:
			run.written++
		}
		if err := j.config.Files.Record(ctx, editOf[k].MediaObjectID, now, digests[k],
			strings.Join(result.Changed, ","), failure); err != nil {
			return err
		}
	}
	return nil
}

// coverFileNames are the folder pictures players look for; a folder with any of them is left alone.
var coverFileNames = []string{"cover.jpg", "folder.jpg", "front.jpg", "cover.png", "folder.png", "front.png"}

// folderCover puts the album's cover into its folder as cover.jpg (Navidrome reads it), once per folder per run,
// and never where a cover file already is.
func (j *TagWritebackJob) folderCover(ctx context.Context, run *tagRun, folder, cacheKey string) {
	if run.folderCovers[folder] {
		return
	}
	run.folderCovers[folder] = true
	for _, name := range coverFileNames {
		if _, err := j.config.FS.Stat(path.Join(folder, name)); err == nil {
			return
		}
	}
	converted := j.coverJPEG(ctx, run, cacheKey)
	if converted == "" {
		return
	}
	source, err := j.config.FS.Open(converted)
	if err != nil {
		return
	}
	defer func() { _ = source.Close() }()
	if err := fsx.WriteAtomic(j.config.FS, path.Join(folder, "cover.jpg"), func(w io.Writer) error {
		_, err := io.Copy(w, source)
		return err
	}); err != nil {
		j.config.Logger.WarnContext(ctx, "folder cover not written", slog.String("folder", folder), slog.Any("error", err))
	}
}

// tagEditFor is what the options ask of one file, and a digest of it that stays the same until
// the ids, the metadata, the cover or the options change. Edit.Cover holds the cover's key.
func tagEditFor(candidate library.TagCandidate, options TagOptions) (mediatool.TagEdit, string) {
	edit := mediatool.TagEdit{Fill: mediatool.Tags{}, Set: mediatool.Tags{}}
	if options.WriteIDs {
		ids := edit.Fill
		if options.ReplaceIDs {
			ids = edit.Set
		}
		putTag(ids, tagReleaseGroupID, candidate.WorkExternalID)
		putTag(ids, tagReleaseID, candidate.EditionExternalID)
		putTag(ids, tagRecordingID, candidate.PartExternalID)
		putTag(ids, tagAlbumArtistID, candidate.PersonExternalID)
	}
	if options.WriteMetadata {
		putTag(edit.Set, tagAlbum, candidate.WorkTitle)
		putTag(edit.Set, tagAlbumArtist, candidate.ArtistName)
		putTag(edit.Set, tagTitle, candidate.PartTitle)
		putTag(edit.Set, tagGenre, candidate.Genres)
		if candidate.Year > 0 {
			putTag(edit.Set, tagDate, strconv.FormatInt(candidate.Year, 10))
		}
		if disc, track, err := parseMediaRef(candidate.MediaRef); err == nil {
			putTag(edit.Set, tagDiscNumber, strconv.Itoa(disc))
			putTag(edit.Set, tagTrackNumber, strconv.Itoa(track))
		}
	}
	if options.Navidrome {
		// Fill-only: what the file says about its track artists stays; the primary artist is all Solarr knows.
		putTag(edit.Fill, tagArtistID, candidate.PersonExternalID)
		putTag(edit.Fill, "artist", candidate.ArtistName)
		putTag(edit.Fill, "artists", candidate.ArtistName)
		putTag(edit.Fill, "albumartists", candidate.ArtistName)
	}
	coverVersion := ""
	if options.EmbedCover && candidate.CoverKey != "" {
		edit.Cover = candidate.CoverKey
		coverVersion = fmt.Sprintf("%s@%d", candidate.CoverKey, candidate.CoverFetchedAt)
	}

	hash := sha256.New()
	for _, part := range []struct {
		mode string
		tags mediatool.Tags
	}{{"fill", edit.Fill}, {"set", edit.Set}} {
		keys := make([]string, 0, len(part.tags))
		for key := range part.tags {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", part.mode, key, part.tags[key])
		}
	}
	_, _ = fmt.Fprintf(hash, "cover\x00%s\x00navidrome\x00%t", coverVersion, options.Navidrome)
	return edit, hex.EncodeToString(hash.Sum(nil))
}

func putTag(tags mediatool.Tags, key, value string) {
	if value != "" {
		tags[key] = value
	}
}

// coverJPEG converts a fetched cover to the JPEG players read, once per run; empty on failure,
// which leaves that file's picture for a later run.
func (j *TagWritebackJob) coverJPEG(ctx context.Context, run *tagRun, cacheKey string) string {
	if converted, done := run.covers[cacheKey]; done {
		return converted
	}
	destination, err := j.convertCover(cacheKey)
	if err != nil {
		j.config.Logger.WarnContext(ctx, "cover for embedding not prepared", slog.String("cover", cacheKey), slog.Any("error", err))
	}
	run.covers[cacheKey] = destination
	return destination
}

func (j *TagWritebackJob) convertCover(cacheKey string) (string, error) {
	file, err := j.config.FS.Open(filepath.Join(j.config.ImagesDir, cacheKey+"-full.webp"))
	if err != nil {
		return "", err
	}
	source, err := io.ReadAll(io.LimitReader(file, maxImageBytes))
	_ = file.Close()
	if err != nil {
		return "", err
	}
	img, err := artwork.Decode(source)
	if err != nil {
		return "", err
	}
	encoded, err := artwork.EncodeJPEG(img)
	if err != nil {
		return "", err
	}
	if err := j.config.FS.MkdirAll(j.config.CoversDir, 0o755); err != nil {
		return "", err
	}
	destination := filepath.Join(j.config.CoversDir, cacheKey+".jpg")
	if err := fsx.WriteAtomic(j.config.FS, destination, func(w io.Writer) error {
		_, err := w.Write(encoded)
		return err
	}); err != nil {
		return "", err
	}
	return destination, nil
}

// removeCovers deletes the run's JPEG copies: the WebP in the cache stays the one kept copy.
func (j *TagWritebackJob) removeCovers(run *tagRun) {
	for _, converted := range run.covers {
		if converted != "" {
			_ = j.config.FS.Remove(converted)
		}
	}
}

// EnqueueTagWriteback submits one sweep, deduplicated by TagWritebackActionKey.
func EnqueueTagWriteback(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: TagWritebackJobType, ActionKey: TagWritebackActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
}
