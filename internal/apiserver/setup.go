package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/config"
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

	view := s.rootFolderView(created)
	view.Writable = &writable
	writeJSON(w, s.logger, http.StatusCreated, view)
}

// rootFolderView renders one root folder for the API, including its own
// read_only/allow_rename policy, capacity (nil until the capacity job has
// run once), and the categories it is attached to -- everything the root
// folder overview screen (todo 4.18) needs in one call, since
// internal/capacity already computes every number and nothing rendered them
// before this.
func (s *Server) rootFolderView(folder settings.RootFolder) gen.RootFolder {
	view := gen.RootFolder{
		Id: folder.ID, Path: folder.Path, Online: folder.Online,
		ReadOnly: &folder.ReadOnly, AllowRename: &folder.AllowRename,
		TotalBytes: folder.TotalBytes, FreeBytes: folder.FreeBytes,
		MinFreeBytes: &folder.MinFreeBytes,
	}
	if folder.Label != "" {
		label := folder.Label
		view.Label = &label
	}
	if folder.CapacityCheckedAt != nil {
		checkedAt := time.Unix(*folder.CapacityCheckedAt, 0).UTC()
		view.CapacityCheckedAt = &checkedAt
	}
	if folder.DrainState != "" {
		state := gen.RootFolderDrainState(folder.DrainState)
		view.DrainState = &state
	}
	if folder.DrainJobID != nil {
		view.DrainJobId = folder.DrainJobID
	}

	snapshot := s.auth.Settings.Snapshot()
	var categories []gen.RootFolderCategory
	for _, categoryID := range snapshot.CategoriesForRootFolder(folder.ID) {
		if category, found := snapshot.Category(categoryID); found {
			categories = append(categories, gen.RootFolderCategory{Id: category.ID, Name: category.Name})
		}
	}
	if categories != nil {
		view.Categories = &categories
	}
	return view
}

// routeUpdateRootFolderPolicy parses the id path parameter, mirroring
// routeDrainRootFolder.
func (s *Server) routeUpdateRootFolderPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a root folder id must be a number")
		return
	}
	s.UpdateRootFolderPolicy(w, r, id)
}

// UpdateRootFolderPolicy replaces a root's read_only/allow_rename/
// min_free_bytes wholesale (Invariant 69). Administrator only.
func (s *Server) UpdateRootFolderPolicy(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateRootFolderPolicyJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected read_only, allow_rename and min_free_bytes")
		return
	}

	if err := s.auth.Settings.SetRootFolderPolicy(r.Context(), id, body.ReadOnly, body.AllowRename, body.MinFreeBytes); err != nil {
		if errors.Is(err, settings.ErrNotFound) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such root folder")
			return
		}
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The policy has problems", problems.Report("the root folder", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}

	folder, found := s.auth.Settings.Snapshot().RootFolder(id)
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such root folder")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.rootFolderView(folder))
}

// routeSetRootFolderOnline parses the id path parameter, mirroring
// routeDrainRootFolder.
func (s *Server) routeSetRootFolderOnline(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a root folder id must be a number")
		return
	}
	s.SetRootFolderOnline(w, r, id)
}

// SetRootFolderOnline declares a root online or offline (section 10.4).
// Administrator only. A declared-offline root stays that way indefinitely --
// nothing probes it back online (Invariant 87).
func (s *Server) SetRootFolderOnline(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.SetRootFolderOnlineJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected an online flag")
		return
	}

	if err := s.auth.Settings.SetRootFolderOnline(r.Context(), id, body.Online); err != nil {
		if errors.Is(err, settings.ErrNotFound) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such root folder")
			return
		}
		writeError(w, s.logger, err)
		return
	}

	folder, found := s.auth.Settings.Snapshot().RootFolder(id)
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such root folder")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.rootFolderView(folder))
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
