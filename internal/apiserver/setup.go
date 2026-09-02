package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/settings"
)

// maxSetupBody bounds what an unauthenticated caller can make the server read.
const maxSetupBody = 4 << 10

// minPasswordLength is a floor, not a policy. Composition rules push people
// towards predictable substitutions; length is what actually helps.
const minPasswordLength = 12

// GetSetupStatus reports what the wizard still has to do.
//
// It answers unauthenticated because before the first account exists there is
// nobody to authenticate as. It deliberately reveals nothing but the outstanding
// steps -- no version, no paths, no usernames -- so answering it to anyone costs
// nothing.
func (s *Server) GetSetupStatus(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.auth.Settings.Snapshot()
	writeJSON(w, s.logger, http.StatusOK, gen.SetupStatus{
		NeedsAdministrator: len(snapshot.Users()) == 0,
		HasRootFolder:      len(snapshot.RootFolders()) > 0,
	})
}

// CreateFirstAdministrator claims the install, once.
//
// The window closes permanently on the first account rather than when the
// wizard finishes: tying it to the wizard would leave it open across a restart
// someone abandoned halfway, which is exactly when nobody is watching.
func (s *Server) CreateFirstAdministrator(w http.ResponseWriter, r *http.Request) {
	if len(s.auth.Settings.Snapshot().Users()) > 0 {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"Setup is closed", "an account already exists; sign in instead")
		return
	}

	var request gen.CreateFirstAdministratorJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with username and password")
		return
	}

	username := strings.TrimSpace(request.Username)
	if username == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a username is required")
		return
	}
	if len([]rune(request.Password)) < minPasswordLength {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the password must be at least 12 characters")
		return
	}

	hashed, err := auth.HashPassword(request.Password)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	created, err := s.auth.Settings.CreateUser(r.Context(), s.auth.Clock,
		username, hashed, settings.RoleAdmin)
	if err != nil {
		// A second request that arrived while the first was still writing loses
		// the race here rather than creating a second administrator.
		writeError(w, s.logger, err)
		return
	}

	// Signed in immediately. Asking someone to log in with the credentials they
	// typed ten seconds ago is friction with nothing behind it.
	http.SetCookie(w, s.sessionCookie(s.auth.Sessions.Issue(created.ID),
		int(s.auth.Sessions.TTL().Seconds()), r))
	s.logger.Info("first administrator created", "username", created.Username)

	writeJSON(w, s.logger, http.StatusCreated, gen.Identity{
		UserId: created.ID, Username: created.Username, Role: gen.Role(created.Role),
	})
}

// ListRootFolders returns the configured library roots.
func (s *Server) ListRootFolders(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}

	folders := s.auth.Settings.Snapshot().RootFolders()
	views := make([]gen.RootFolder, 0, len(folders))
	for _, folder := range folders {
		view := rootFolderView(folder)
		if s.roots != nil {
			writable := s.roots.Writable(folder.Path) == nil
			view.Writable = &writable
		}
		views = append(views, view)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// AddRootFolder accepts a library root after proving it is usable.
//
// The probe is the point. A path that does not exist, or a volume the container
// cannot write, is the single most common misconfiguration in a containerised
// deployment, and finding it here rather than at the first import is the
// difference between a typo and a mystery.
func (s *Server) AddRootFolder(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var request gen.AddRootFolderJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with a path")
		return
	}

	if s.roots == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", ErrNoRootChecker.Error())
		return
	}

	path := strings.TrimSpace(request.Path)
	if err := s.roots.Validate(path); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"The folder cannot be used", err.Error())
		return
	}

	// Not writable is reported, not refused. Ownership on the host is the
	// operator's decision, and refusing here would mean a library that exists
	// and is simply owned by someone else could not be configured at all --
	// while the import that eventually needs to write it is far away in time.
	writable := true
	if err := s.roots.Writable(path); err != nil {
		writable = false
		s.logger.Warn("root folder is not writable by this process",
			"path", path, "detail", err.Error())
	}

	var label string
	if request.Label != nil {
		label = strings.TrimSpace(*request.Label)
	}

	created, err := s.auth.Settings.CreateRootFolder(r.Context(), path, label)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	view := rootFolderView(created)
	view.Writable = &writable
	writeJSON(w, s.logger, http.StatusCreated, view)
}

func rootFolderView(folder settings.RootFolder) gen.RootFolder {
	view := gen.RootFolder{Id: folder.ID, Path: folder.Path, Online: folder.Online}
	if folder.Label != "" {
		label := folder.Label
		view.Label = &label
	}
	return view
}

// RootChecker inspects a candidate library root.
//
// An interface so apiserver does not reach for the filesystem itself, and so a
// test can make a path fail without arranging a real unwritable directory.
type RootChecker interface {
	// Validate refuses a path that cannot be a root at all.
	Validate(path string) error
	// Writable reports whether this process can write there. A failure is
	// advisory: it is flagged, never enforced.
	Writable(path string) error
}

// ErrNoRootChecker reports a server built without one.
var ErrNoRootChecker = errors.New("apiserver: no root checker configured")
