package arrimport

import (
	"context"
	"encoding/json"
	"strings"
)

// Naming is one *Arr's naming settings in the shape every app is read into: each format filed under the key of the Solarr
// naming slot it fills, so one translation serves every app.
type Naming struct {
	RenameFiles, ReplaceIllegalCharacters bool
	// ColonReplacement is Lidarr's numbering (0 delete, 1 dash, 2 space dash, 3 space dash space, 4 smart); -1 is one Solarr has no counterpart for.
	ColonReplacement int
	Formats          map[string]string
}

// Naming answers Lidarr's naming, its formats filed under music's slots.
func (c LidarrClient) Naming(ctx context.Context) (Naming, error) {
	config, err := c.NamingConfig(ctx)
	if err != nil {
		return Naming{}, err
	}
	return Naming{
		RenameFiles: config.RenameTracks, ReplaceIllegalCharacters: config.ReplaceIllegalCharacters, ColonReplacement: config.ColonReplacementFormat,
		Formats: map[string]string{"artist_folder": config.ArtistFolderFormat, "standard_track": config.StandardTrackFormat, "multi_disc_track": config.MultiDiscTrackFormat},
	}, nil
}

// colonNumber reads a colon replacement an app writes as a number (Lidarr, Sonarr) or a word (Radarr, Whisparr v3).
func colonNumber(raw json.RawMessage) int {
	var number int
	if json.Unmarshal(raw, &number) == nil {
		return number
	}
	var word string
	if json.Unmarshal(raw, &word) != nil {
		return -1
	}
	switch strings.ToLower(word) {
	case "delete":
		return 0
	case "dash":
		return 1
	case "spacedash":
		return 2
	case "spacedashspace":
		return 3
	case "smart":
		return 4
	}
	return -1
}

// namingWire is GET config/naming for the apps that share Sonarr's and Radarr's shape; each fills the fields of its own kind.
type namingWire struct {
	RenameEpisodes, RenameMovies, RenameBooks bool
	ReplaceIllegalCharacters                  bool
	ColonReplacementFormat                    json.RawMessage
	StandardEpisodeFormat                     string
	DailyEpisodeFormat                        string
	AnimeEpisodeFormat                        string
	SeriesFolderFormat                        string
	SeasonFolderFormat                        string
	SpecialsFolderFormat                      string
	StandardMovieFormat                       string
	MovieFolderFormat                         string
	StandardBookFormat                        string
	AuthorFolderFormat                        string
}

func (c LidarrClient) namingWire(ctx context.Context, path string) (namingWire, error) {
	var raw map[string]json.RawMessage
	if err := c.get(ctx, path, &raw); err != nil {
		return namingWire{}, err
	}
	text := func(key string) string {
		var value string
		_ = json.Unmarshal(raw[key], &value)
		return value
	}
	flag := func(key string) bool {
		var value bool
		_ = json.Unmarshal(raw[key], &value)
		return value
	}
	return namingWire{
		RenameEpisodes: flag("renameEpisodes"), RenameMovies: flag("renameMovies"), RenameBooks: flag("renameBooks"),
		ReplaceIllegalCharacters: flag("replaceIllegalCharacters"), ColonReplacementFormat: raw["colonReplacementFormat"],
		StandardEpisodeFormat: text("standardEpisodeFormat"), DailyEpisodeFormat: text("dailyEpisodeFormat"), AnimeEpisodeFormat: text("animeEpisodeFormat"),
		SeriesFolderFormat: text("seriesFolderFormat"), SeasonFolderFormat: text("seasonFolderFormat"), SpecialsFolderFormat: text("specialsFolderFormat"),
		StandardMovieFormat: text("standardMovieFormat"), MovieFolderFormat: text("movieFolderFormat"),
		StandardBookFormat: text("standardBookFormat"), AuthorFolderFormat: text("authorFolderFormat"),
	}, nil
}

func (w namingWire) colon() int {
	if len(w.ColonReplacementFormat) == 0 {
		return -1
	}
	return colonNumber(w.ColonReplacementFormat)
}

// Naming answers Sonarr's naming under the TV slots.
func (c SonarrClient) Naming(ctx context.Context) (Naming, error) {
	wire, err := c.inner.namingWire(ctx, "/api/v3/config/naming")
	if err != nil {
		return Naming{}, err
	}
	return Naming{
		RenameFiles: wire.RenameEpisodes, ReplaceIllegalCharacters: wire.ReplaceIllegalCharacters, ColonReplacement: wire.colon(),
		Formats: map[string]string{
			"standard_episode": wire.StandardEpisodeFormat, "daily_episode": wire.DailyEpisodeFormat, "anime_episode": wire.AnimeEpisodeFormat,
			"series_folder": wire.SeriesFolderFormat, "season_folder": wire.SeasonFolderFormat, "specials_folder": wire.SpecialsFolderFormat,
		},
	}, nil
}

// Naming answers Radarr's naming under the movie slots.
func (c RadarrClient) Naming(ctx context.Context) (Naming, error) {
	wire, err := c.inner.namingWire(ctx, "/api/v3/config/naming")
	if err != nil {
		return Naming{}, err
	}
	return Naming{
		RenameFiles: wire.RenameMovies, ReplaceIllegalCharacters: wire.ReplaceIllegalCharacters, ColonReplacement: wire.colon(),
		Formats: map[string]string{"standard_movie": wire.StandardMovieFormat, "movie_folder": wire.MovieFolderFormat},
	}, nil
}

// Naming answers Readarr's naming under the book slots.
func (c ReadarrClient) Naming(ctx context.Context) (Naming, error) {
	wire, err := c.inner.namingWire(ctx, "/api/v1/config/naming")
	if err != nil {
		return Naming{}, err
	}
	return Naming{
		RenameFiles: wire.RenameBooks, ReplaceIllegalCharacters: wire.ReplaceIllegalCharacters, ColonReplacement: wire.colon(),
		Formats: map[string]string{"standard_book": wire.StandardBookFormat, "author_folder": wire.AuthorFolderFormat},
	}, nil
}

// Naming answers Whisparr's naming under the scene slots: v2 is Sonarr-shaped (episodes in series folders), v3 Radarr-shaped.
func (c WhisparrClient) Naming(ctx context.Context) (Naming, error) {
	wire, err := c.inner.namingWire(ctx, "/api/v3/config/naming")
	if err != nil {
		return Naming{}, err
	}
	scene, folder, rename := wire.StandardEpisodeFormat, wire.SeriesFolderFormat, wire.RenameEpisodes
	if wire.StandardMovieFormat != "" {
		scene, folder, rename = wire.StandardMovieFormat, wire.MovieFolderFormat, wire.RenameMovies
	}
	return Naming{
		RenameFiles: rename, ReplaceIllegalCharacters: wire.ReplaceIllegalCharacters, ColonReplacement: wire.colon(),
		Formats: map[string]string{"standard_scene": scene, "studio_folder": folder},
	}, nil
}
