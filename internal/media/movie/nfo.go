package movie

import (
	"encoding/xml"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// maxNFOBytes bounds a movie nfo that is read; a real one is a few kilobytes.
const maxNFOBytes = 1 << 20

// nfoMovie is the ids of a Kodi or Jellyfin movie nfo, in the current form (uniqueid) and the legacy tags.
type nfoMovie struct {
	UniqueIDs []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
	TMDBID string `xml:"tmdbid"`
	IMDbID string `xml:"imdbid"`
	ID     string `xml:"id"`
}

// NFOIDs reads the movie ids a file's nfo names: "<file name>.nfo" beside it, else "movie.nfo". A missing or
// unreadable file names none.
func NFOIDs(fsys fsx.FS, filePath string) IDs {
	stem := strings.TrimSuffix(filePath, path.Ext(filePath))
	for _, candidate := range []string{stem + ".nfo", path.Join(path.Dir(filePath), "movie.nfo")} {
		if ids := readNFO(fsys, candidate); ids != (IDs{}) {
			return ids
		}
	}
	return IDs{}
}

func readNFO(fsys fsx.FS, name string) IDs {
	file, err := fsys.Open(name)
	if err != nil {
		return IDs{}
	}
	defer func() { _ = file.Close() }()
	var movie nfoMovie
	if xml.NewDecoder(io.LimitReader(file, maxNFOBytes)).Decode(&movie) != nil {
		return IDs{}
	}
	var ids IDs
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
	ids.TMDB, ids.IMDb = number(movie.TMDBID), imdb(movie.IMDbID)
	if ids.IMDb == "" {
		ids.IMDb = imdb(movie.ID)
	}
	for _, unique := range movie.UniqueIDs {
		switch strings.ToLower(unique.Type) {
		case "tmdb":
			ids.TMDB = max(ids.TMDB, number(unique.Value))
		case "imdb":
			if ids.IMDb == "" {
				ids.IMDb = imdb(unique.Value)
			}
		}
	}
	return ids
}
