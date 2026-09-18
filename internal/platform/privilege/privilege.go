// Package privilege hands the process to its runtime user.
// See documentation/container-runtime.md.
package privilege

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// DefaultUID and DefaultGID apply only when PUID and PGID are unset. The image
// declares neither, so a set variable means the operator asked for it.
const (
	DefaultUID = 1000
	DefaultGID = 1000
)

// Lookup reads the environment, injected so a test need not mutate the process.
type Lookup func(string) (string, bool)

// Identity is who the process should end up as.
type Identity struct {
	UID int
	GID int
	// Requested distinguishes an identity asked for from one defaulted to.
	Requested bool
}

// Outcome describes what happened, for logging and for tests.
type Outcome struct {
	// Dropped is false when the process was already unprivileged.
	Dropped  bool
	Identity Identity
	// Chowned lists the directories whose ownership was changed.
	Chowned []string
}

// Resolve reads PUID and PGID. A bad value is an error, not a silent fallback.
func Resolve(lookup Lookup) (Identity, error) {
	uid, uidSet, err := numeric(lookup, "PUID", DefaultUID)
	if err != nil {
		return Identity{}, err
	}
	gid, gidSet, err := numeric(lookup, "PGID", DefaultGID)
	if err != nil {
		return Identity{}, err
	}

	return Identity{UID: uid, GID: gid, Requested: uidSet || gidSet}, nil
}

func numeric(lookup Lookup, name string, fallback int) (int, bool, error) {
	raw, set := lookup(name)
	if !set || raw == "" {
		return fallback, false, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, fmt.Errorf("%s must be a number, got %q", name, raw)
	}
	// Root is what this package exists to leave.
	if value <= 0 {
		return 0, false, fmt.Errorf("%s must be greater than 0, got %d -- "+
			"Solarr does not run as root; set it to the owner of your volumes", name, value)
	}
	return value, true, nil
}

// Apply takes ownership of directories and drops to identity. A no-op when the
// process is already unprivileged. See documentation/container-runtime.md.
func Apply(fsys fsx.FS, identity Identity, directories []string, logger *slog.Logger) (Outcome, error) {
	if syscall.Geteuid() != 0 {
		current := Identity{UID: syscall.Getuid(), GID: syscall.Getgid()}

		if current.UID != identity.UID || current.GID != identity.GID {
			logger.Warn("PUID/PGID were set but cannot be applied",
				slog.Int("requested_uid", identity.UID),
				slog.Int("requested_gid", identity.GID),
				slog.Int("running_as_uid", current.UID),
				slog.Int("running_as_gid", current.GID),
				slog.String("cause", "the container was started with --user (or runAsUser), "+
					"so the process is not root and cannot change uid"),
				slog.String("fix", "remove --user and let PUID/PGID do it, or set --user to "+
					"match them. Nothing was chowned, so the volumes must already be owned "+
					"by the user shown above"))
			return Outcome{Identity: current}, nil
		}

		logger.Info("running as the requested user; nothing to do",
			slog.Int("uid", current.UID),
			slog.Int("gid", current.GID),
			slog.String("note", "already unprivileged, so no ownership was changed; "+
				"the volumes must already be owned by this user"))
		return Outcome{Identity: current}, nil
	}

	outcome := Outcome{Dropped: true, Identity: identity}
	for _, directory := range directories {
		changed, err := chownTree(fsys, directory, identity)
		if err != nil {
			return Outcome{}, fmt.Errorf("privilege: take ownership of %s: %w", directory, err)
		}
		if changed {
			outcome.Chowned = append(outcome.Chowned, directory)
		}
	}

	if err := drop(identity); err != nil {
		// Continuing as root would defeat the point of starting as root.
		return Outcome{}, err
	}

	logger.Info("running as the media user",
		slog.Int("uid", identity.UID),
		slog.Int("gid", identity.GID),
		slog.Any("took_ownership_of", outcome.Chowned),
		slog.String("note", "PUID/PGID set this. Volumes Solarr did not create "+
			"are left alone; mismatches are reported, not corrected"))
	return outcome, nil
}

// drop relinquishes root permanently. Groups then gid then uid: setuid is what
// removes the ability to make the other two calls.
func drop(identity Identity) error {
	if err := syscall.Setgroups([]int{identity.GID}); err != nil {
		return fmt.Errorf("privilege: set groups to %d: %w", identity.GID, err)
	}
	if err := syscall.Setgid(identity.GID); err != nil {
		return fmt.Errorf("privilege: become gid %d: %w", identity.GID, err)
	}
	if err := syscall.Setuid(identity.UID); err != nil {
		return fmt.Errorf("privilege: become uid %d: %w", identity.UID, err)
	}

	// Verified, because a silent failure runs everything as root regardless.
	if syscall.Geteuid() != identity.UID || syscall.Getuid() != identity.UID {
		return errors.New("privilege: still root after dropping; refusing to continue")
	}
	return nil
}

// chownTree gives one managed directory and its contents to identity. Media
// roots are never passed in. See documentation/container-runtime.md.
func chownTree(fsys fsx.FS, root string, identity Identity) (bool, error) {
	if _, err := fsys.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Created later by the section 14.3 checks, as the dropped user.
			return false, nil
		}
		return false, err
	}

	// Walked every time: a directory's owner says nothing about its contents.
	return true, walk(fsys, root, identity)
}

func walk(fsys fsx.FS, path string, identity Identity) error {
	if err := fsys.Chown(path, identity.UID, identity.GID); err != nil {
		return err
	}

	entries, err := fsys.ReadDir(path)
	if err != nil {
		// A file rather than a directory: already chowned above.
		return nil //nolint:nilerr // ReadDir on a regular file is not a failure here
	}
	for _, entry := range entries {
		if err := walk(fsys, filepath.Join(path, entry.Name), identity); err != nil {
			return err
		}
	}
	return nil
}

// IdentityHint names the identity a permission error was refused for.
func IdentityHint() string {
	return fmt.Sprintf("running as uid=%d gid=%d; check PUID/PGID and the directory's owner",
		syscall.Getuid(), syscall.Getgid())
}

// WarnIfUnapplicable reports before any filesystem work that the requested
// identity is unreachable. See documentation/container-runtime.md.
func WarnIfUnapplicable(identity Identity, out io.Writer) {
	if syscall.Geteuid() == 0 || !identity.Requested {
		return
	}
	current := Identity{UID: syscall.Getuid(), GID: syscall.Getgid()}
	if current.UID == identity.UID && current.GID == identity.GID {
		return
	}

	_, _ = fmt.Fprintf(out,
		"warning: PUID=%d PGID=%d were set, but this container is running as uid=%d gid=%d.\n"+
			"         Changing user needs root, and --user (or runAsUser) already took that\n"+
			"         away, so PUID and PGID cannot be applied and nothing will be chowned.\n"+
			"         Remove --user and let PUID/PGID do it, or set --user to match them.\n\n",
		identity.UID, identity.GID, current.UID, current.GID)
}
