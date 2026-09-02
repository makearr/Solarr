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

// GetSetupStatus reports what the wizard still has to do. Unauthenticated, and
// reveals only the outstanding steps.
func (s *Server) GetSetupStatus(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.auth.Settings.Snapshot()
	writeJSON(w, s.logger, http.StatusOK, gen.SetupStatus{
		NeedsAdministrator: len(snapshot.Users()) == 0,
		HasRootFolder:      len(snapshot.RootFolders()) > 0,
	})
}

// CreateFirstAdministrator claims the install once. The window closes on the
// first account, not when the wizard finishes.
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
	// No length or composition rule. Rules of that shape produce predictable
	// substitutions rather than strength, and this is a self-hosted install
	// whose owner is the only person they would constrain. Empty is still
	// refused: an account nobody can fail to authenticate as is auth_mode none,
	// which is a deliberate file-level decision, not an empty form field.
	if request.Password == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a password is required")
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
		// A concurrent second claim loses the race here.
		writeError(w, s.logger, err)
		return
	}

	// Signed in immediately; re-typing the credentials adds nothing.
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

	writeJSON(w, s.logger, http.StatusOK, s.rootFolderViews())
}

// AddRootFolder accepts a library root, refusing an unusable path and flagging
// an unwritable one. See documentation/container-runtime.md.
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

	// Reported, not refused: host ownership is the operator's decision.
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

// RootChecker inspects a candidate library root, so this package never reaches
// for the filesystem itself.
type RootChecker interface {
	// Validate refuses a path that cannot be a root at all.
	Validate(path string) error
	// Writable reports whether this process can write there. A failure is
	// advisory: it is flagged, never enforced.
	Writable(path string) error
}

// ErrNoRootChecker reports a server built without one.
var ErrNoRootChecker = errors.New("apiserver: no root checker configured")
