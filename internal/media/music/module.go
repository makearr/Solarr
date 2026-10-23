package music

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/settings"
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
	Sweep         scan.Sweep
	// Hunt searches for one wanted album and sends the best release; nil leaves music unhunted.
	Hunt func(ctx context.Context, itemID int64) (plan.Outcome, error)
	// Adder looks a work up and adds it; nil leaves the type without an add.
	Adder media.Adding
	// Files places a library file nothing identified; nil leaves such files to wait.
	Files media.PlacingFiles
	// Pictures lists and keeps the providers' pictures of a work; nil leaves the picture unchosen.
	Pictures media.Picturing
	// Refresh reads one album again from its sources; nil leaves it unrefreshable.
	Refresh func(ctx context.Context, workID int64) (duplicate bool, err error)
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

// LookupWorks implements media.Adding.
func (m Module) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	if m.Adder == nil {
		return nil, media.ErrNotWired
	}
	return m.Adder.LookupWorks(ctx, lookup)
}

// AddWork implements media.Adding.
func (m Module) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	if m.Adder == nil {
		return media.AddResult{}, media.ErrNotWired
	}
	return m.Adder.AddWork(ctx, request)
}

// PlaceFile implements media.PlacingFiles.
func (m Module) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	if m.Files == nil {
		return 0, 0, media.ErrNotWired
	}
	return m.Files.PlaceFile(ctx, categoryID, mediaObjectID, placement, wholeFolder)
}

// WorkPictures implements media.Picturing.
func (m Module) WorkPictures(ctx context.Context, workID int64) ([]media.ImageOption, error) {
	if m.Pictures == nil {
		return nil, media.ErrNotWired
	}
	return m.Pictures.WorkPictures(ctx, workID)
}

// PickWorkPicture implements media.Picturing.
func (m Module) PickWorkPicture(ctx context.Context, workID int64, imageURL string) error {
	if m.Pictures == nil {
		return media.ErrNotWired
	}
	return m.Pictures.PickWorkPicture(ctx, workID, imageURL)
}

// RefreshWork implements media.Refreshing.
func (m Module) RefreshWork(ctx context.Context, workID int64) (bool, error) {
	if m.Refresh == nil {
		return false, media.ErrNotWired
	}
	return m.Refresh(ctx, workID)
}

// AfterMerge implements media.Merging: the files an album took over are placed on its own tracklist.
func (m Module) AfterMerge(ctx context.Context, keepID int64) error {
	if m.Realign == nil {
		return nil
	}
	return m.Realign(ctx, keepID)
}

// HuntSearch implements media.Hunting.
func (m Module) HuntSearch(ctx context.Context, itemID int64) (plan.Outcome, error) {
	if m.Hunt == nil {
		return plan.OutcomeNotWanted, nil
	}
	return m.Hunt(ctx, itemID)
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
	for _, registration := range m.Registrations {
		if err := register(registration.Definition, registration.Handler); err != nil {
			return err
		}
	}
	return nil
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
	{Key: media.FileRescanAfterRefresh, Label: "Rescan Artist Folder after Refresh", Help: "Rescan the artist's folders after refreshing its metadata; otherwise changes to files on disk are not noticed after a refresh."},
	{Key: media.FileChangeFileDate, Label: "Change File Date", Help: "Change the file date on import and rescan.", Options: []media.Choice{{Key: "none", Label: "None"}, {Key: "release_date", Label: "Album Release Date"}}},
	{Key: media.FileImportExtraFiles, Label: "Import Extra Files", Help: "Bring matching files along with the tracks: lyrics, cue sheets, rip logs."},
	{Key: media.FileExtraFileExtensions, Label: "Extra File Extensions", Help: "Comma separated, such as lrc,cue,log,nfo. A file named like a track takes the track's new name; others keep theirs."},
}

// scanMonitoring is what a scan does with an album it finds that nothing wanted yet.
var scanMonitoring = media.ScanMonitor{Setting: media.ScanMonitorDiscovered, Label: "Monitor discovered", Options: []media.Choice{
	{Key: "monitored", Label: "Monitored"}, {Key: "unmonitored", Label: "Unmonitored"}, {Key: "none", Label: "None"},
}}

func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:   MediaType,
		Label: "Music",
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
		ExtraFiles:      media.ExtraFilesDefault{Extensions: []string{"lrc", "cue", "log", "nfo"}},
		FileSettings:    fileSettings,
		ScanMonitor:     scanMonitoring,
		SearchesLibrary: true,
		CoverSources:    true,
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
