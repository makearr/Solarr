// Package sidecar writes the files media servers read beside a library's files: nfo documents and pictures, never over
// a file that is there and never on a root Solarr may not write to.
package sidecar

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Writer puts sidecar files in place; Picture answers a provider picture's bytes as served, nil writes no pictures.
type Writer struct {
	FS      fsx.FS
	Picture func(ctx context.Context, address string) ([]byte, error)
}

// UniqueID is one provider id as both Jellyfin and Kodi read it.
type UniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

// Image is one provider picture of a work, by kind.
type Image struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// FirstImage is the index of the first image of the kind that has an address, -1 when none.
func FirstImage(images []Image, kind string) int {
	for index, image := range images {
		if image.Kind == kind && image.URL != "" {
			return index
		}
	}
	return -1
}

// WritableRoot says Solarr may write on the root: it is online, probed writable and not declared read only.
func WritableRoot(root settings.RootFolder) bool {
	return root.Online && root.Writable && !root.ReadOnly
}

// Document encodes an nfo: the XML header, then the value indented.
func Document(value any) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(xml.Header)
	encoder := xml.NewEncoder(&out)
	encoder.Indent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	out.WriteString("\n")
	return out.Bytes(), nil
}

// Once writes the file when nothing is at the path, answering 1 when it did.
func (w Writer) Once(target string, content func() ([]byte, error)) (int, error) {
	if _, err := w.FS.Lstat(target); err == nil {
		return 0, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	body, err := content()
	if err != nil {
		return 0, err
	}
	if err := fsx.WriteAtomic(w.FS, target, func(out io.Writer) error {
		_, err := out.Write(body)
		return err
	}); err != nil {
		return 0, fmt.Errorf("sidecar: write %s: %w", target, err)
	}
	return 1, nil
}

// AnyExists says whether a picture of the name is there in either format a person may have put it in.
func (w Writer) AnyExists(folder, name string) bool {
	for _, extension := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		if _, err := w.FS.Lstat(path.Join(folder, name+extension)); err == nil {
			return true
		}
	}
	return false
}

// WritePicture writes one provider picture under the name in the format it is served in; failing to get it is skipped.
func (w Writer) WritePicture(ctx context.Context, folder, name, address string) int {
	if w.Picture == nil {
		return 0
	}
	extension := strings.ToLower(path.Ext(strings.SplitN(address, "?", 2)[0]))
	if extension != ".png" {
		extension = ".jpg"
	}
	made, err := w.Once(path.Join(folder, name+extension), func() ([]byte, error) { return w.Picture(ctx, address) })
	if err != nil {
		return 0
	}
	return made
}
