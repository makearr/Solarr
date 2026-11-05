package probe

import (
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

var subtitleSidecarExtensions = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".vtt": true, ".sup": true}

// sidecarSubtitleLanguages lists the languages of the subtitle files that sit beside a video, named like it
// ("Show - S01E01.eng.srt", "Show - S01E01.srt"): a file with no language in its name counts as "und".
func sidecarSubtitleLanguages(fsys fsx.FS, videoPath string) []string {
	if fsys == nil {
		return nil
	}
	entries, err := fsys.ReadDir(path.Dir(videoPath))
	if err != nil {
		return nil
	}
	base := strings.TrimSuffix(path.Base(videoPath), path.Ext(videoPath))
	var languages []string
	for _, entry := range entries {
		extension := strings.ToLower(path.Ext(entry.Name))
		if entry.IsDir || !subtitleSidecarExtensions[extension] || !strings.HasPrefix(entry.Name, base+".") {
			continue
		}
		middle := ""
		if withoutExtension := strings.TrimSuffix(entry.Name, path.Ext(entry.Name)); len(withoutExtension) > len(base) {
			middle = withoutExtension[len(base)+1:]
		}
		language := "und"
		// "x." stands in for the title, which the parser never reads as a claim.
		if info, _ := parsetv.ParseSubtitleFile("x." + middle + extension); info.Language != "" {
			language = info.Language
		}
		languages = append(languages, language)
	}
	return languages
}
