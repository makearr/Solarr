package music

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parse"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// MediaType is music's key: category.media_type and work.media_type carry it.
const MediaType media.Key = "music"

// Module is music as the media registry sees it. The zero value declares the type; Sweep makes it scannable.
type Module struct {
	// Registrations are the job types music brings, with their handlers.
	Registrations []media.JobRegistration
	// Wiring is the add, pictures, refresh and hunt services.
	media.Wiring
	Sweep scan.Sweep
	// Files places a library file nothing identified; nil leaves such files to wait.
	Files media.PlacingFiles
	// Realign places an album's claimed files on its tracks; nil leaves merged files where they were.
	Realign func(ctx context.Context, workID int64) error
	// Rename is what renaming a music library needs; nil leaves music without a rename.
	Rename *RenameConfig
	// Feed is music's part of the RSS sync.
	Feed
	// CutoffJob is music's part of the cutoff check.
	CutoffJob
	// Following is music's part of a follow.
	Following
	// Passes are the type's periodic metadata passes.
	media.Passes
	// Searching is music's part of a search a person asks for.
	Searching
}

// PlaceFile implements media.PlacingFiles.
func (m Module) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	if m.Files == nil {
		return 0, 0, media.ErrNotWired
	}
	return m.Files.PlaceFile(ctx, categoryID, mediaObjectID, placement, wholeFolder)
}

// AfterMerge implements media.Merging: the files an album took over are placed on its own tracklist.
func (m Module) AfterMerge(ctx context.Context, keepID int64) error {
	if m.Realign == nil {
		return nil
	}
	return m.Realign(ctx, keepID)
}

// NameFiles implements media.Renaming.
func (m Module) NameFiles(ctx context.Context, category settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	if m.Rename == nil {
		return nil, errors.New("music: rename is not wired")
	}
	return m.Rename.NameFiles(ctx, category, workID, files)
}

// RegisterJobs implements media.Jobs.
func (m Module) RegisterJobs(register media.JobRegistrar) error {
	return media.RegisterAll(register, m.Registrations)
}

// ScanSweep implements media.Scanning.
func (m Module) ScanSweep() scan.Sweep { return m.Sweep }

// Descriptor implements media.Module.
// fileSettings are Lidarr's Media Management settings as a music library has them.
var fileSettings = []media.FileSetting{
	{Key: media.FileCreateEmptyLeadFolders, Label: "Create Empty Artist Folders", Help: "Create missing artist folders for monitored artists during disk scan."},
	{Key: media.FileDeleteEmptyFolders, Label: "Delete Empty Folders", Help: "Delete empty artist and album folders during disk scan and when track files are deleted."},
	{Key: media.FileUnmonitorDeleted, Label: "Unmonitor Deleted Tracks", Help: "An album with a track deleted from disk is automatically unmonitored."},
	{Key: media.FileMergeLeadAliases, Label: "Merge Artists Stored Under Several Names", Help: "After each disk scan, an artist found under several folder names or aliases becomes one artist: the identified artist wins, otherwise the name with the most albums. Files stay where they are."},
	{Key: media.FileRescanAfterRefresh, Label: "Rescan Artist Folder after Refresh", Help: "Rescan the artist's folders after refreshing its metadata; otherwise changes to files on disk are not noticed until the next scan. An automatic refresh reads only the folders of files that met no track."},
	{Key: media.FileChangeFileDate, Label: "Change File Date", Help: "Change the file date on import and rescan.", Options: []media.Choice{{Key: "none", Label: "None"}, {Key: "release_date", Label: "Album Release Date"}}},
	{Key: media.FileImportExtraFiles, Label: "Import Extra Files", Help: "Bring matching files along with the tracks: lyrics, cue sheets, rip logs."},
	{Key: media.FileExtraFileExtensions, Label: "Extra File Extensions", Help: "Comma separated, such as lrc,cue,log,nfo. A file named like a track takes the track's new name; others keep theirs."},
}

// scanMonitoring is what a scan does with an album it finds that nothing wanted yet.
var scanMonitoring = media.ScanMonitor{Setting: media.ScanMonitorDiscovered, Label: "Monitor discovered", Options: []media.Choice{
	{Key: "monitored", Label: "Monitored"}, {Key: "unmonitored", Label: "Unmonitored"}, {Key: "none", Label: "None"},
}}

// RecogniseRelease implements media.RecognisingReleases: the format and bitrate a name states, MP3 for a bare bitrate as the search does.
func (Module) RecogniseRelease(title string) []media.ReleaseClaim {
	attributes := parse.Parse(title).Attributes
	var out []media.ReleaseClaim
	add := func(key, value string) {
		if value != "" {
			out = append(out, media.ReleaseClaim{Key: key, Value: value})
		}
	}
	format := attributes.Format
	if format == "" && !attributes.Lossless && (attributes.BitrateKbps > 0 || attributes.VBRPreset != "") {
		format = "mp3"
	}
	add("format", format)
	if attributes.Lossless {
		add("lossless", "yes")
	}
	if attributes.BitrateKbps > 0 {
		add("bitrate", strconv.Itoa(attributes.BitrateKbps)+" kbps")
	}
	add("vbr_preset", attributes.VBRPreset)
	if attributes.BitDepth > 0 {
		add("bit_depth", strconv.Itoa(attributes.BitDepth)+"-bit")
	}
	if attributes.SampleRate > 0 {
		add("sample_rate", strconv.FormatFloat(float64(attributes.SampleRate)/1000, 'f', -1, 64)+" kHz")
	}
	add("source", attributes.Source)
	return out
}

func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:       MediaType,
		Label:     "Music",
		GenreTree: GenreTree,
		Vocabulary: media.Vocabulary{
			Work:         media.Term{One: "Album", Many: "Albums"},
			Part:         media.Term{One: "Track", Many: "Tracks"},
			Group:        media.Term{One: "Disc", Many: "Discs"},
			LeadPeople:   media.Term{One: "Artist", Many: "Artists"},
			Organisation: media.Term{One: "Label", Many: "Labels"},
		},
		Levels: Navigation{}.Levels(),
		Roles: []media.Role{
			{Key: "artist", Label: "Artist", Lead: true, Followable: true},
		},
		FollowOptions:      followOptions,
		Facets:             facets,
		SearchTerms:        termChoices,
		WorkImage:          media.ImageCover,
		AddKinds:           []media.Choice{{Key: "album", Label: "Albums"}, {Key: "artist", Label: "Artists"}},
		CommitsHeldImports: true,
		DuplicatesByName:   true,
		// Lidarr's: off, and when on lyrics, cue sheets, rip logs and release notes.
		ExtraFiles:        media.ExtraFilesDefault{Extensions: []string{"lrc", "cue", "log", "nfo"}},
		FileSettings:      fileSettings,
		ScanMonitor:       scanMonitoring,
		UnidentifiedRetry: media.IdentifyRetryKey,
		SearchesLibrary:   true,
		CoverSources:      true,
	}
}

// PartPosition implements media.Numbering: "disc2:track3" is track 3 under "Disc 2", as Lidarr heads a release's discs.
func (Module) PartPosition(ref string) (section, number string) {
	var disc, track int
	if _, err := fmt.Sscanf(ref, "disc%d:track%d", &disc, &track); err != nil {
		return "", ref
	}
	return "Disc " + strconv.Itoa(disc), strconv.Itoa(track)
}

// NamingScheme implements media.Naming.
func (Module) NamingScheme() naming.Scheme { return NamingScheme }

// DefaultNamingProfile implements media.Naming.
func (Module) DefaultNamingProfile() settings.NamingProfile { return DefaultNamingProfile() }

// ValidateNaming implements media.Naming.
func (Module) ValidateNaming(profile settings.NamingProfile) error { return ValidateNaming(profile) }

// NamingExamples implements media.Naming.
func (Module) NamingExamples(profile settings.NamingProfile) []media.NamingExample {
	var out []media.NamingExample
	for _, example := range NamingExamples(NamingFor(&profile)) {
		out = append(out, media.NamingExample{Label: example.Label, Path: example.Path})
	}
	return out
}
