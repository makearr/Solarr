// Package linker turns the second path of one media object into a link to the first, so a file two folders hold
// byte for byte is stored once (ADR-11, Invariant 92). Plan only reads; Apply verifies the bytes again and swaps
// the path atomically, so a crash leaves either the old file or the link, never a gap.
package linker

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Method is how two paths come to share bytes.
type Method string

// Reflink shares blocks yet stays an independent file; Hardlink makes both paths one file.
const (
	Reflink  Method = "reflink"
	Hardlink Method = "hardlink"
)

// ladder is the order the filesystem is asked: independence first (ADR-11).
var ladder = []Method{Reflink, Hardlink}

// ErrContentDiffers means the two files are not the same bytes after all, so nothing was replaced.
var ErrContentDiffers = errors.New("linker: the two files differ")

// ErrNoMethod means the filesystem offers neither reflink nor hardlink between the pair.
var ErrNoMethod = errors.New("linker: neither reflink nor hardlink is possible between the two paths")

// tempSuffix names the link under construction beside its destination, so the final rename is on one filesystem.
const tempSuffix = ".solarr-link-tmp"

// Path is one place a media object lives.
type Path struct {
	PathID       int64
	RootFolderID int64
	Abs          string
}

// Object is one media object and the places it lives, the one to keep first.
type Object struct {
	MediaObjectID int64
	SizeBytes     int64
	Paths         []Path
}

// Link is one planned replacement: Replace becomes a Method link to Keep.
type Link struct {
	MediaObjectID int64
	Keep, Replace Path
	SizeBytes     int64
	Method        Method
}

// Skipped is a path left alone, and why.
type Skipped struct {
	MediaObjectID int64
	Path          Path
	Reason        string
}

// Reasons a path is left alone.
const (
	ReasonMissing         = "missing"
	ReasonSizeChanged     = "size-changed"
	ReasonOtherFilesystem = "other-filesystem"
	ReasonNoMethod        = "no-method"
)

// Plan decides, for every object, which paths can become links to its first readable path. A path already on the
// keeper's inode needs nothing; one on another filesystem or unreadable is reported, never an error.
func Plan(fsys fsx.FS, objects []Object) ([]Link, []Skipped, error) {
	var links []Link
	var skipped []Skipped
	for _, object := range objects {
		var keep *Path
		var keepInfo fsx.FileInfo
		var keepFilesystem string
		for index := range object.Paths {
			candidate := object.Paths[index]
			info, err := fsys.Stat(candidate.Abs)
			if err != nil {
				skipped = append(skipped, Skipped{object.MediaObjectID, candidate, ReasonMissing})
				continue
			}
			if info.Size != object.SizeBytes {
				skipped = append(skipped, Skipped{object.MediaObjectID, candidate, ReasonSizeChanged})
				continue
			}
			filesystem, err := fsys.FilesystemID(candidate.Abs)
			if err != nil {
				return nil, nil, err
			}
			if keep == nil {
				keep, keepInfo, keepFilesystem = &object.Paths[index], info, filesystem
				continue
			}
			if filesystem != keepFilesystem {
				skipped = append(skipped, Skipped{object.MediaObjectID, candidate, ReasonOtherFilesystem})
				continue
			}
			if info.Inode == 0 || info.Inode == keepInfo.Inode {
				continue
			}
			method, ok := probe(fsys, keep.Abs, candidate.Abs)
			if !ok {
				skipped = append(skipped, Skipped{object.MediaObjectID, candidate, ReasonNoMethod})
				continue
			}
			links = append(links, Link{
				MediaObjectID: object.MediaObjectID, Keep: *keep, Replace: candidate, SizeBytes: object.SizeBytes, Method: method,
			})
		}
	}
	return links, skipped, nil
}

// probe asks the filesystem which method works between the pair by making the link beside the destination and
// removing it again.
func probe(fsys fsx.FS, keep, replace string) (Method, bool) {
	scratch := replace + ".solarr-probe"
	for _, method := range ladder {
		if err := place(fsys, method, keep, scratch); err == nil {
			_ = fsys.Remove(scratch)
			return method, true
		}
	}
	return "", false
}

func place(fsys fsx.FS, method Method, keep, destination string) error {
	if method == Reflink {
		return fsys.Reflink(keep, destination)
	}
	return fsys.Link(keep, destination)
}

// Apply replaces link.Replace with a link to link.Keep after comparing every byte. It answers the method used;
// a pair already sharing an inode is a no-op that answers the method planned.
func Apply(fsys fsx.FS, link Link) (Method, error) {
	keepInfo, err := fsys.Stat(link.Keep.Abs)
	if err != nil {
		return "", fmt.Errorf("linker: %w", err)
	}
	replaceInfo, err := fsys.Stat(link.Replace.Abs)
	if err != nil {
		return "", fmt.Errorf("linker: %w", err)
	}
	if keepInfo.Size != link.SizeBytes || replaceInfo.Size != link.SizeBytes {
		return "", ErrContentDiffers
	}
	if keepInfo.Inode != 0 && keepInfo.Inode == replaceInfo.Inode {
		return link.Method, nil
	}
	same, err := sameBytes(fsys, link.Keep.Abs, link.Replace.Abs)
	if err != nil {
		return "", err
	}
	if !same {
		return "", ErrContentDiffers
	}
	temp := link.Replace.Abs + tempSuffix
	_ = fsys.Remove(temp)
	for _, method := range ladder {
		if err := place(fsys, method, link.Keep.Abs, temp); err != nil {
			continue
		}
		if err := fsys.Rename(temp, link.Replace.Abs); err != nil {
			_ = fsys.Remove(temp)
			return "", fmt.Errorf("linker: replace %s: %w", path.Base(link.Replace.Abs), err)
		}
		return method, nil
	}
	return "", ErrNoMethod
}

// sameBytes compares two files block by block.
func sameBytes(fsys fsx.FS, first, second string) (bool, error) {
	a, err := fsys.Open(first)
	if err != nil {
		return false, fmt.Errorf("linker: %w", err)
	}
	defer func() { _ = a.Close() }()
	b, err := fsys.Open(second)
	if err != nil {
		return false, fmt.Errorf("linker: %w", err)
	}
	defer func() { _ = b.Close() }()
	const block = 1 << 20
	left, right := make([]byte, block), make([]byte, block)
	for {
		readLeft, errLeft := io.ReadFull(a, left)
		readRight, errRight := io.ReadFull(b, right)
		if readLeft != readRight || !bytes.Equal(left[:readLeft], right[:readRight]) {
			return false, nil
		}
		if errLeft != nil || errRight != nil {
			if isEnd(errLeft) && isEnd(errRight) {
				return true, nil
			}
			return false, errors.Join(unexpected(errLeft), unexpected(errRight))
		}
	}
}

func isEnd(err error) bool { return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) }

func unexpected(err error) error {
	if err == nil || isEnd(err) {
		return nil
	}
	return err
}
