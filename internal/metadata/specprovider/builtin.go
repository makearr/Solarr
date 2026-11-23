package specprovider

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed builtin/*.yaml
var builtinFiles embed.FS

// Builtin is the definition files that ship, by key: the providers that are only a file.
func Builtin() map[string][]byte {
	files := map[string][]byte{}
	entries, err := fs.ReadDir(builtinFiles, "builtin")
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if data, err := builtinFiles.ReadFile("builtin/" + entry.Name()); err == nil {
			files[strings.TrimSuffix(entry.Name(), ".yaml")] = data
		}
	}
	return files
}
