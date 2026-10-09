package music

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parse"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

// Providers of an unverified album's identity (D6): its own tags or folder, never a metadata source.
// An album with only these ids is unverified.
const (
	ProviderLocalAlbum  = "local:album"
	ProviderLocalArtist = "local:artist"
)

// unverifiedConfidence is below every threshold: a local identity is a name, not a match.
const unverifiedConfidence = 0.1

// LocalNames are the artist and album an unidentified unit calls itself: tags first, then its folder
// read as a release name.
func LocalNames(ctx context.Context, sourceRoot string, unit inspect.Unit, tags Resolved) (artist, album string, err error) {
	if len(unit.Media) > 0 {
		representative, err := tags.TagsFor(ctx, model.FileRef{RelPath: unit.Media[0].RelPath})
		if err != nil {
			return "", "", err
		}
		artist = representative.First("albumartist", "album_artist", "artist")
		album = representative.First("album")
	}
	folderArtist, folderAlbum := folderNames(sourceRoot, unit.Directory)
	if artist == "" {
		artist = folderArtist
	}
	if album == "" {
		album = folderAlbum
	}
	return strings.TrimSpace(artist), strings.TrimSpace(album), nil
}

// folderNames reads a unit's own folder as a release name: the folder itself, not its parent, and not
// "." when the imported folder is the album.
func folderNames(sourceRoot, directory string) (artist, album string) {
	folder := path.Base(path.Join(sourceRoot, directory))
	parsed := parse.Parse(folder).Identity
	album = parsed.Album
	if strings.TrimSpace(album) == "" {
		album = folder
	}
	return parsed.Artist, album
}

// localAlbumKey is one unverified album per source folder (D17): two folders with the same sloppy
// tags stay two albums until identified; the same folder imported again is the same album.
func localAlbumKey(sourceRoot, directory, album string) string {
	return strings.ToLower(path.Join(sourceRoot, directory)) + "|" + strings.ToLower(album)
}

// CommitUnverifiedUnit places a unit nothing identified as an unverified album named by its own tags or
// folder (D6): visible, never searched for (D17), identified later from the review.
func (c Committer) CommitUnverifiedUnit(
	ctx context.Context, sourceRoot string, unit inspect.Unit, tags Resolved, categoryID int64, ladder []string,
) (Placed, error) {
	artist, album, err := LocalNames(ctx, sourceRoot, unit, tags)
	if err != nil {
		return Placed{}, fmt.Errorf("music: unverified unit %q: %w", unit.Directory, err)
	}
	identity := model.Identification{
		Strategy: "unverified", Confidence: unverifiedConfidence,
		ExternalID: &model.ExternalID{
			Provider: ProviderLocalAlbum, ExternalID: localAlbumKey(sourceRoot, unit.Directory, album),
			Confidence: unverifiedConfidence,
		},
	}
	placed, err := c.CommitMatchedUnit(ctx, sourceRoot, unit, identity, tags, categoryID, nil, ladder)
	if err != nil {
		return Placed{}, err
	}
	items, err := c.desired.AlbumItemsForWork(ctx, placed.WorkID)
	if err != nil {
		return placed, fmt.Errorf("music: unverified unit %q: %w", unit.Directory, err)
	}
	for _, item := range items {
		if err := c.desired.SetItemMonitored(ctx, item.ID, false); err != nil {
			return placed, fmt.Errorf("music: unverified unit %q: unmonitor: %w", unit.Directory, err)
		}
	}
	if artist != "" && c.people != nil {
		personID, err := c.people.ResolveFolderArtist(ctx, c.clock.Now(), ProviderLocalArtist, artist)
		if err != nil {
			return placed, fmt.Errorf("music: unverified unit %q: artist: %w", unit.Directory, err)
		}
		if err := c.people.AddLeadCredit(ctx, placed.WorkID, personID, CreditRoleArtist); err != nil {
			return placed, fmt.Errorf("music: unverified unit %q: credit: %w", unit.Directory, err)
		}
	}
	return placed, nil
}

// IdentifyUnverified moves an unverified album's placed files onto the album chosen for them (D6's merge);
// the files stay put and a rename preview brings their paths in line.
func (c Committer) IdentifyUnverified(
	ctx context.Context, fromWorkID int64, best model.Identification, categoryID int64,
) (Placed, error) {
	if best.ExternalID == nil {
		return Placed{}, fmt.Errorf("music: identify unverified work %d: %w", fromWorkID, errNoExternalID)
	}
	work, err := c.identity.WorkIdentityOf(ctx, fromWorkID)
	if err != nil {
		return Placed{}, err
	}
	if known, found, err := c.identity.KnownWork(ctx, best.ExternalID.Provider, best.ExternalID.ExternalID); err != nil {
		return Placed{}, fmt.Errorf("music: identify unverified work %d: %w", fromWorkID, err)
	} else if found {
		work.CanonicalTitle, work.SortTitle = known.Title, SortTitle(known.Title)
	}
	locations, err := c.identity.WorkFileRoots(ctx, fromWorkID)
	if err != nil {
		return Placed{}, err
	}
	if len(locations) == 0 {
		return Placed{}, fmt.Errorf("music: identify unverified work %d: it holds no files", fromWorkID)
	}
	links := make([]library.WorkLink, len(locations))
	mediaObjectIDs := make([]int64, len(locations))
	for index, location := range locations {
		mediaObjectIDs[index] = location.MediaObjectID
		links[index] = library.WorkLink{
			Provider: best.ExternalID.Provider, ExternalID: best.ExternalID.ExternalID,
			Confidence: best.ExternalID.Confidence, Work: work,
			RootFolderID: location.RootFolderID, MediaObjectID: location.MediaObjectID,
		}
	}
	// Linked to the chosen album first, so a failure never leaves the files belonging to nothing.
	resolutions, err := c.identity.ResolveWorksDetailed(ctx, c.clock.Now(), links)
	if err != nil {
		return Placed{}, fmt.Errorf("music: identify unverified work %d: %w", fromWorkID, err)
	}
	if _, err := c.identity.ReleaseWork(ctx, fromWorkID); err != nil {
		return Placed{}, err
	}
	target := resolutions[0]
	if err := DiscoverWanted(ctx, c.desired, c.categories, c.clock, c.defaultProfileID, categoryID, target.WorkID, nil); err != nil {
		return Placed{}, fmt.Errorf("music: identify unverified work %d: %w", fromWorkID, err)
	}
	return Placed{
		WorkID: target.WorkID, WorkCreated: target.WorkCreated, EditionID: target.EditionID,
		EditionCreated: target.EditionCreated, MediaObjectIDs: mediaObjectIDs, RootFolderID: locations[0].RootFolderID,
	}, nil
}
