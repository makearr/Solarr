package tv

import (
	"encoding/xml"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// maxNFOBytes bounds a tvshow.nfo that is read; a real one is a few kilobytes.
const maxNFOBytes = 1 << 20

// nfoShow is the ids of a Kodi or Jellyfin tvshow.nfo, in the current form (uniqueid) and the legacy tags.
type nfoShow struct {
	UniqueIDs []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
	TVDBID string `xml:"tvdbid"`
	TMDBID string `xml:"tmdbid"`
	IMDbID string `xml:"imdb_id"`
}

// ShowNFOIDs reads the series ids a folder's tvshow.nfo names; a missing or unreadable file names none.
func ShowNFOIDs(fsys fsx.FS, folder string) FolderGuess {
	file, err := fsys.Open(path.Join(folder, "tvshow.nfo"))
	if err != nil {
		return FolderGuess{}
	}
	defer func() { _ = file.Close() }()
	var show nfoShow
	if xml.NewDecoder(io.LimitReader(file, maxNFOBytes)).Decode(&show) != nil {
		return FolderGuess{}
	}
	ids := FolderGuess{}
	number := func(text string) int64 {
		id, _ := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		return max(id, 0)
	}
	imdb := func(text string) string {
		text = strings.ToLower(strings.TrimSpace(text))
		if strings.HasPrefix(text, "tt") && number(strings.TrimPrefix(text, "tt")) > 0 {
			return text
		}
		return ""
	}
	ids.TVDBID, ids.TMDBID, ids.IMDbID = number(show.TVDBID), number(show.TMDBID), imdb(show.IMDbID)
	for _, unique := range show.UniqueIDs {
		switch strings.ToLower(unique.Type) {
		case "tvdb":
			ids.TVDBID = max(ids.TVDBID, number(unique.Value))
		case "tmdb":
			ids.TMDBID = max(ids.TMDBID, number(unique.Value))
		case "imdb":
			if ids.IMDbID == "" {
				ids.IMDbID = imdb(unique.Value)
			}
		}
	}
	return ids
}
