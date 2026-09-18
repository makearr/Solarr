package music

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// Naming tokens this media type declares (ADR-23: an extension point, not a
// switch in internal/moving/naming, which knows nothing about music). Release
// Group has no source yet -- media_attributes' claimed side is not wired up
// (todo 4.2's own remaining item) -- declared here so a template naming it
// compiles today and starts working the moment that lands, rather than
// needing every shipped profile edited twice.
const (
	TokenArtistName    = "Artist Name"
	TokenAlbumTitle    = "Album Title"
	TokenReleaseYear   = "Release Year"
	TokenDisc          = "disc"
	TokenTrack         = "track"
	TokenTrackPosition = "Track Position"
	TokenTrackTitle    = "Track Title"
	TokenFormat        = "Format"
	TokenReleaseGroup  = "Release Group"
)

// NamingTokens is every token name NamingContext can populate, for
// naming.ValidateTokens -- the compile-time "unknown token" check section
// 11.8 asks for.
var NamingTokens = map[string]bool{
	TokenArtistName: true, TokenAlbumTitle: true, TokenReleaseYear: true,
	TokenDisc: true, TokenTrack: true, TokenTrackPosition: true, TokenTrackTitle: true,
	TokenFormat: true, TokenReleaseGroup: true,
}

// DefaultFolderTemplate and DefaultFileTemplate are section 11.8's shipped
// music default -- chosen to reproduce the existing library byte for byte,
// so adopting it proposes zero renames.
const (
	DefaultFolderTemplate = "{" + TokenArtistName + "}/{" + TokenAlbumTitle + "} ({" + TokenReleaseYear + "})"
	DefaultFileTemplate   = "{" + TokenArtistName + "} - {" + TokenTrackPosition + "} - {" + TokenTrackTitle + "}"
)

// mediaRefPattern extracts music.Consumer's own "disc%d:track%d" convention
// (internal/media/music/enrich.go's mediaRef) -- the one place that format is
// parsed back apart, so it stays in this package rather than a second copy
// of the convention living in internal/moving/naming, which must not know what a
// disc or a track even is.
var mediaRefPattern = regexp.MustCompile(`^disc(\d+):track(\d+)$`)

// NamingContext builds a naming.Context for one track (work_part) from the
// same WorkDetail the work detail page already assembles, plus which
// edition and part within it are being named. Returns an error only when
// part does not belong to detail at all -- a caller error, not a missing
// field (an absent field is simply omitted from the context, which
// naming.Render already treats as "not available").
func NamingContext(detail library.WorkDetail, edition library.EditionDetail, part library.PartDetail) (naming.Context, error) {
	ctx := naming.Context{
		TokenAlbumTitle: detail.Work.CanonicalTitle,
	}

	if artist, found := firstArtistCredit(detail.Credits); found {
		ctx[TokenArtistName] = artist
	}
	if year, ok := releaseYear(detail, edition); ok {
		ctx[TokenReleaseYear] = strconv.Itoa(year)
	}
	if edition.Edition.Format.Valid {
		ctx[TokenFormat] = edition.Edition.Format.String
	}
	if part.Part.Title.Valid {
		ctx[TokenTrackTitle] = part.Part.Title.String
	}

	disc, track, err := parseMediaRef(part.Part.MediaRef)
	if err != nil {
		return nil, fmt.Errorf("music: naming context: %w", err)
	}
	ctx[TokenDisc] = strconv.Itoa(disc)
	ctx[TokenTrack] = strconv.Itoa(track)
	ctx[TokenTrackPosition] = TrackPosition(disc, track, DiscCount(detail.Parts))

	return ctx, nil
}

// DiscCount is how many discs a release spans, counting only parts whose media_ref parses.
func DiscCount(parts []library.PartDetail) int {
	discs := make(map[int]struct{}, 1)
	for _, part := range parts {
		if disc, _, err := parseMediaRef(part.Part.MediaRef); err == nil {
			discs[disc] = struct{}{}
		}
	}
	return len(discs)
}

// TrackPosition is the track's printed position: "06", or "1-06" once a release has more
// than one disc, so a multi-disc set does not repeat every number in one folder.
func TrackPosition(disc, track, discCount int) string {
	if discCount > 1 && disc > 0 {
		return fmt.Sprintf("%d-%02d", disc, track)
	}
	return fmt.Sprintf("%02d", track)
}

func firstArtistCredit(credits []library.Credit) (string, bool) {
	for _, credit := range credits {
		if credit.Role == CreditRoleArtist {
			return credit.PersonName, true
		}
	}
	return "", false
}

// releaseYear prefers the edition's own year over the work's: work.Year is
// the release group's original release date (EnrichJob's refinementFor sets
// it from FirstReleaseDate), while edition.Year is this specific pressing's
// -- a reissue folder conventionally names the pressing's own year, not the
// original album's. Falls back to the work's year when the edition does not
// state one.
func releaseYear(detail library.WorkDetail, edition library.EditionDetail) (int, bool) {
	if edition.Edition.Year.Valid {
		return int(edition.Edition.Year.Int64), true
	}
	if detail.Work.Year.Valid {
		return int(detail.Work.Year.Int64), true
	}
	return 0, false
}

func parseMediaRef(mediaRef string) (disc, track int, err error) {
	match := mediaRefPattern.FindStringSubmatch(mediaRef)
	if match == nil {
		return 0, 0, fmt.Errorf("media_ref %q does not match \"discN:trackM\"", mediaRef)
	}
	disc, _ = strconv.Atoi(match[1])
	track, _ = strconv.Atoi(match[2])
	return disc, track, nil
}
