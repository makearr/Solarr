package fsx

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxComponentBytes is the ext4/XFS/ZFS limit for one path component.
const maxComponentBytes = 255

// separatorReplacement keeps a name readable when it contains a path separator,
// and makes the substitution visible rather than silently merging words:
// "XT/QK" becomes "XT_QK", not "XTQK".
const separatorReplacement = '_'

// reservedNames are illegal as filenames on Windows whatever their extension.
// The library is stored on Linux but read over SMB by Jellyfin, Kodi and
// Navidrome clients, where a file called CON.mkv is unopenable.
var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM0": true, "COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT0": true, "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SanitiseComponent makes one path component safe to place beneath a root:
// separators become '_', control characters are dropped, trailing dots and
// spaces are removed, Windows reserved names gain a trailing underscore, and
// the result is capped at 255 bytes without splitting a rune. "." and ".."
// sanitise to the empty string, which SafeJoin rejects.
//
// Truncation cuts the tail, so a caller building a filename passes the base
// name and the extension separately rather than one joined string — otherwise a
// long title would take the extension with it.
func SanitiseComponent(component string) string {
	var b strings.Builder
	b.Grow(len(component))
	for _, r := range component {
		switch {
		case r == '/' || r == '\\':
			b.WriteRune(separatorReplacement)
		case r < 0x20 || r == 0x7f:
			// Control characters, including NUL, are dropped outright.
		case r == utf8.RuneError:
			// Invalid UTF-8 would corrupt the byte-wise length cap below.
		default:
			b.WriteRune(r)
		}
	}

	cleaned := strings.Trim(b.String(), " ")
	cleaned = truncateBytes(cleaned, maxComponentBytes)
	// Trailing dots and spaces are stripped by Windows on creation, so a name
	// written with them is not the name that comes back.
	cleaned = strings.TrimRight(cleaned, " .")

	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return ""
	}
	// The underscore goes after the base name, not after the extension, or a
	// reserved name would cost the file its extension.
	if base, extension, hasExtension := strings.Cut(cleaned, "."); reservedNames[strings.ToUpper(base)] {
		if hasExtension {
			return base + "_." + extension
		}
		return base + "_"
	}
	return cleaned
}

// truncateBytes caps s at limit bytes without splitting a rune.
func truncateBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// SafeJoin builds an absolute path beneath root and proves it stays there
// (Invariant 20). Every component is sanitised, the result is resolved through
// any symlinks, and a path landing outside root is ErrEscapesRoot.
//
// Sanitising alone would be enough against a traversal in the component text;
// resolving is what catches a symlink already on disk pointing out of the root,
// which is the case a template renderer cannot see.
func SafeJoin(fsys FS, root string, components ...string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: %q", ErrNotAbsolute, root)
	}
	resolvedRoot, err := fsys.Resolve(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve root %q: %w", root, err)
	}

	safe := make([]string, 0, len(components)+1)
	safe = append(safe, resolvedRoot)
	for _, component := range components {
		sanitised := SanitiseComponent(component)
		if sanitised == "" {
			return "", fmt.Errorf("%w: %q", ErrEmptyComponent, component)
		}
		safe = append(safe, sanitised)
	}

	resolved, err := fsys.Resolve(filepath.Join(safe...))
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", filepath.Join(safe...), err)
	}
	if !Within(resolvedRoot, resolved) {
		return "", fmt.Errorf("%w: %q is not inside %q", ErrEscapesRoot, resolved, resolvedRoot)
	}
	return resolved, nil
}

// Within reports whether path is root itself or lies beneath it. Both must
// already be cleaned and resolved; comparing unresolved paths is how a symlink
// slips past this check.
func Within(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}
