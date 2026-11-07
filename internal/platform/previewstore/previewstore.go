// Package previewstore is where the preview pictures of a video file live on disk.
package previewstore

import (
	"path/filepath"
	"strconv"
)

// The two pictures made of a video.
const (
	Frame = "frame"
	Sheet = "sheet"
)

// Folder is where a file's pictures live: a folder per thousand ids keeps any one folder small.
func Folder(dir string, mediaObjectID int64) string {
	return filepath.Join(dir, strconv.FormatInt(mediaObjectID/1000, 10))
}

// Path is one picture of a file, a JPEG.
func Path(dir string, mediaObjectID int64, kind string) string {
	return filepath.Join(Folder(dir, mediaObjectID), strconv.FormatInt(mediaObjectID, 10)+"-"+kind+".jpg")
}
