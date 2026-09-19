package apiserver

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// browseMaxEntries bounds one listing: a directory with a hundred thousand
// files is a real thing on a media host, and the picker only ever shows a
// page of them.
const browseMaxEntries = 500

// BrowsePath lists one directory so a path can be picked rather than
// remembered. Administrator only -- it reports what exists on the host, the
// same trust boundary adding a root folder already carries.
func (s *Server) BrowsePath(w http.ResponseWriter, r *http.Request, params gen.BrowsePathParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.fsys == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "browsing is not available")
		return
	}

	asked := "/"
	if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
		asked = path.Clean(strings.TrimSpace(*params.Path))
	}
	if !path.IsAbs(asked) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a path must be absolute")
		return
	}

	directory, prefix := s.browseTarget(asked)
	entries, err := s.fsys.ReadDir(directory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound,
				"Not found", "no such directory")
			return
		}
		writeError(w, s.logger, err)
		return
	}

	dirsOnly := params.DirsOnly != nil && *params.DirsOnly
	views := make([]gen.PathEntry, 0, len(entries))
	truncated := false
	for _, entry := range entries {
		if dirsOnly && !entry.IsDir {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(entry.Name), strings.ToLower(prefix)) {
			continue
		}
		if len(views) == browseMaxEntries {
			truncated = true
			break
		}
		views = append(views, gen.PathEntry{
			Name: entry.Name, Path: path.Join(directory, entry.Name), IsDir: entry.IsDir,
		})
	}
	// Directories first, then by name: the picker is walked downwards, so a
	// folder among ten thousand tracks has to stay reachable.
	sort.SliceStable(views, func(a, b int) bool {
		if views[a].IsDir != views[b].IsDir {
			return views[a].IsDir
		}
		return strings.ToLower(views[a].Name) < strings.ToLower(views[b].Name)
	})

	listing := gen.PathListing{Path: directory, Entries: views}
	if truncated {
		listing.Truncated = &truncated
	}
	if directory != "/" {
		parent := path.Dir(directory)
		listing.Parent = &parent
	}
	writeJSON(w, s.logger, http.StatusOK, listing)
}

// browseTarget resolves what to actually list: the path itself when it is a
// directory, otherwise the directory above it with the typed remainder as a
// name prefix -- which is what makes one endpoint serve both browsing and
// typeahead.
func (s *Server) browseTarget(asked string) (directory, prefix string) {
	if info, err := s.fsys.Stat(asked); err == nil && info.IsDir {
		return asked, ""
	}
	if asked == "/" {
		return "/", ""
	}
	return path.Dir(asked), path.Base(asked)
}

// routeBrowsePath parses the query parameters, mirroring routeListImportSessions.
func (s *Server) routeBrowsePath(w http.ResponseWriter, r *http.Request) {
	var params gen.BrowsePathParams
	if raw := r.URL.Query().Get("path"); raw != "" {
		params.Path = &raw
	}
	if r.URL.Query().Get("dirs_only") == "true" {
		dirsOnly := true
		params.DirsOnly = &dirsOnly
	}
	s.BrowsePath(w, r, params)
}
