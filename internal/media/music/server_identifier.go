package music

import (
	"context"
	"path"

	"github.com/makearr/Solarr/internal/identify/model"
)

// StrategyMediaServer names ids a media server holds for the album a file sits in.
const StrategyMediaServer = "media-server"

// OrderMediaServer puts the server's ids after the file's own tags and before any search.
const OrderMediaServer = 30

// The confidences are below a tag's: the server may have been tagged by a different tool than this file.
const (
	confidenceServerReleaseGroup = 0.95
	confidenceServerRelease      = 0.94
)

// ServerAlbumIDs are the MusicBrainz ids a media server holds for an album.
type ServerAlbumIDs struct{ ReleaseGroup, Release string }

// ServerAsker answers what the servers a library takes metadata from hold of the album at an absolute path; none is no match.
type ServerAsker interface {
	AlbumIDsByPath(ctx context.Context, categoryID int64, absPath string) (ServerAlbumIDs, bool)
}

// ServerIdentifier names a file by its album folder's ids on a media server, for files whose own tags carry none.
// It asks once per folder: a batch is a folder's files.
type ServerIdentifier struct {
	asker    ServerAsker
	category int64
	absolute func(relPath string) string
	seen     map[string]serverAnswer
}

type serverAnswer struct {
	ids   ServerAlbumIDs
	found bool
}

// NewServerIdentifier builds it; absolute turns a file's relative path into the path the server's mapping is read against.
func NewServerIdentifier(asker ServerAsker, categoryID int64, absolute func(relPath string) string) ServerIdentifier {
	return ServerIdentifier{asker: asker, category: categoryID, absolute: absolute, seen: map[string]serverAnswer{}}
}

// Name identifies the strategy in a Decision.
func (ServerIdentifier) Name() string { return "media-server-ids" }

// Order is OrderMediaServer.
func (ServerIdentifier) Order() int { return OrderMediaServer }

// Identify prefers the release group, as the tags do.
func (s ServerIdentifier) Identify(ctx context.Context, file model.FileRef) ([]model.Identification, error) {
	folder := path.Dir(s.absolute(file.RelPath))
	answer, asked := s.seen[folder]
	if !asked {
		answer.ids, answer.found = s.asker.AlbumIDsByPath(ctx, s.category, s.absolute(file.RelPath))
		s.seen[folder] = answer
	}
	if !answer.found {
		return nil, nil
	}
	var provider, external string
	var confidence float64
	switch group, release := validMBID(answer.ids.ReleaseGroup), validMBID(answer.ids.Release); {
	case group != "":
		provider, external, confidence = ProviderReleaseGroup, group, confidenceServerReleaseGroup
	case release != "":
		provider, external, confidence = ProviderRelease, release, confidenceServerRelease
	default:
		return nil, nil
	}
	return []model.Identification{{
		Strategy:   StrategyMediaServer,
		ExternalID: &model.ExternalID{Provider: provider, ExternalID: external, Confidence: 1.0},
		Precedence: model.PrecedenceAsserted,
		Confidence: confidence,
		Evidence:   []model.MatchEvidence{{Kind: "media_server", Detail: provider + "=" + external}},
	}}, nil
}
