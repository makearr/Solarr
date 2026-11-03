package tv

import (
	"context"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/metadata/thexem"
)

// SceneSource is TheXEM as the service needs it: a series' scene numbering and the scene names it is released under.
type SceneSource interface {
	Mappings(ctx context.Context, tvdbID int64) ([]thexem.Mapping, error)
	Names(ctx context.Context, tvdbID int64) ([]string, error)
}

// addSceneNumbers gives each episode the number scene releases carry for it. TheXEM maps TheTVDB's aired order only, so a
// series numbered by any other list is left without; a failure keeps the numbers stored before.
func (s Service) addSceneNumbers(ctx context.Context, series *Series) {
	if s.Scene == nil || series.IDs.TVDB <= 0 || (series.Basis != "" && series.Basis != ProviderTVDB+":aired") {
		return
	}
	mappings, err := s.Scene.Mappings(ctx, series.IDs.TVDB)
	if err != nil {
		s.warn(ProviderXEM, series.IDs.TVDB, err)
		return
	}
	byTVDB := make(map[Key]thexem.Number, len(mappings))
	for _, mapping := range mappings {
		byTVDB[Key{mapping.TVDB.Season, mapping.TVDB.Episode}] = mapping.Scene
	}
	series.SceneMapped = true
	for index := range series.Episodes {
		episode := &series.Episodes[index]
		episode.Scene = nil
		if scene, mapped := byTVDB[Key{episode.Season, episode.Number}]; mapped {
			episode.Scene = &SceneNumber{Season: scene.Season, Number: scene.Episode, Absolute: scene.Absolute}
		}
	}
	if len(mappings) == 0 {
		return
	}
	names, err := s.Scene.Names(ctx, series.IDs.TVDB)
	if err != nil {
		s.warn(ProviderXEM, series.IDs.TVDB, err)
		return
	}
	for _, name := range names {
		if name != series.Title && !slices.Contains(series.Aliases, name) {
			series.Aliases = append(series.Aliases, name)
		}
	}
}

// sceneNumbers reads a release's numbers as the scene's where TheXEM maps them, else as TheTVDB's own (pain point 30).
type sceneNumbers struct {
	// fromScene and fromAbsolute turn a scene number into TheTVDB's; toScene is the other way.
	fromScene    map[Key]Key
	fromAbsolute map[int]Key
	toScene      map[Key]SceneNumber
}

func newSceneNumbers(episodes []library.SeriesEpisode) sceneNumbers {
	numbers := sceneNumbers{fromScene: map[Key]Key{}, fromAbsolute: map[int]Key{}, toScene: map[Key]SceneNumber{}}
	for _, episode := range episodes {
		if episode.Scene == "" {
			continue
		}
		tvdb := Key{episode.Season, episode.Number}
		season, number := splitRef(episode.Scene)
		scene := SceneNumber{Season: season, Number: number}
		if absolute, err := strconv.Atoi(episode.SceneAbsolute); err == nil && absolute > 0 {
			scene.Absolute = absolute
			numbers.fromAbsolute[absolute] = tvdb
		}
		numbers.fromScene[Key{season, number}] = tvdb
		numbers.toScene[tvdb] = scene
	}
	return numbers
}

func (n sceneNumbers) mapped() bool { return len(n.toScene) > 0 }

// episode is TheTVDB's number for a release's season and episode.
func (n sceneNumbers) episode(season, number int) Key {
	if tvdb, mapped := n.fromScene[Key{season, number}]; mapped {
		return tvdb
	}
	return Key{season, number}
}

// scene is the number scene releases carry for one of TheTVDB's episodes.
func (n sceneNumbers) scene(tvdb Key) SceneNumber {
	if scene, mapped := n.toScene[tvdb]; mapped {
		return scene
	}
	return SceneNumber{Season: tvdb.Season, Number: tvdb.Number}
}

// partKeys is partKeys for a downloaded release, its season, episode and absolute numbers read as the scene's.
func (n sceneNumbers) partKeys(info parsetv.Info) []library.PartKey {
	if !n.mapped() {
		return partKeys(info)
	}
	var keys []library.PartKey
	switch {
	case len(info.EpisodeNumbers) > 0:
		for _, number := range info.EpisodeNumbers {
			tvdb := n.episode(info.SeasonNumber, number)
			keys = append(keys, library.PartKey{MediaRef: MediaRef(tvdb.Season, tvdb.Number)})
		}
	case len(info.AbsoluteNumbers) > 0:
		for _, number := range info.AbsoluteNumbers {
			if tvdb, mapped := n.fromAbsolute[number]; mapped {
				keys = append(keys, library.PartKey{MediaRef: MediaRef(tvdb.Season, tvdb.Number)})
			} else {
				keys = append(keys, library.PartKey{AltScheme: "absolute", AltRef: strconv.Itoa(number)})
			}
		}
	default:
		return partKeys(info)
	}
	return keys
}
