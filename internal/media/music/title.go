package music

import (
	"path"
	"strings"
)

// albumTitleFallback names a work when no album tag was readable: the containing
// directory, which is how a Lidarr-managed library already names an album folder.
func albumTitleFallback(relPath string) string {
	dir := path.Dir(relPath)
	if dir == "." || dir == "/" {
		return strings.TrimSuffix(path.Base(relPath), path.Ext(relPath))
	}
	return path.Base(dir)
}
