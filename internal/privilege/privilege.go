// Package privilege hands the process to its runtime user.
//
// The container image starts as root so it can take ownership of its volumes on
// behalf of whoever the operator wants to run as -- the PUID/PGID convention the
// *Arr ecosystem established, and what section 14.3's error messages already
// tell people to reach for. It then drops, permanently, before anything else
// happens. The application never runs as root.
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

	"github.com/makearr/MediaNexus/internal/fsx"
)

// DefaultUID and DefaultGID are the `media` user built into the image.
//
// They are only what happens when PUID and PGID are unset. Whenever they are
// set they govern both halves of the question -- the user the process becomes
// and the ownership it writes -- and nothing else overrides them. 1000 is the
// first ordinary account on a Linux system, so on a single-user NAS the default
// is usually already the right answer.
//
// This is the only place the default is written. The image deliberately does
// not declare it as an environment variable, so that a variable being present
// means somebody asked for it.
const (
	DefaultUID = 1000
	DefaultGID = 1000
)

// Lookup reads the environment. Injected rather than read directly so a test
// does not have to mutate the process (ADR-17).
type Lookup func(string) (string, bool)

// Identity is who the process should end up as.
type Identity struct {
	UID int
	GID int
	// Requested is true when the operator set PUID or PGID rather than taking
	// the default. It is the difference between "we chose 1000" and "they asked
	// for 1000", and only the second is a request that can be disappointed.
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

// Resolve reads PUID and PGID, falling back to the image's `media` user.
//
// An unparseable value is an error rather than a silent fallback: a typo in
// PUID would otherwise run as 1000 and produce a permission failure much later,
// pointing at the volume rather than at the typo.
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
	// 0 would mean "run as root", which this package exists to prevent. Refusing
	// is safer than honouring it: someone setting PUID=0 is working around a
	// permission problem that chowning the volume would actually fix.
	if value <= 0 {
		return 0, false, fmt.Errorf("%s must be greater than 0, got %d -- "+
			"MediaNexus does not run as root; set it to the owner of your volumes", name, value)
	}
	return value, true, nil
}

// Apply takes ownership of directories and drops to identity.
//
// It is a no-op when the process is already unprivileged, which is what happens
// when the operator passes --user: an explicit choice outranks PUID/PGID, and a
// process that is not root cannot chown anything anyway.
func Apply(fsys fsx.FS, identity Identity, directories []string, logger *slog.Logger) (Outcome, error) {
	if syscall.Geteuid() != 0 {
		current := Identity{UID: syscall.Getuid(), GID: syscall.Getgid()}

		// PUID and PGID govern both the runtime user and ownership, and this is
		// the one situation where they cannot: changing uid needs root, and the
		// container was handed to us as somebody else. That is the container
		// runtime's decision, not something to paper over -- so when the
		// operator asked for an identity we cannot deliver, say exactly that
		// rather than running as a different user in silence.
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
		// Continuing as root is not an option: the whole point of starting as
		// root was to stop being it.
		return Outcome{}, err
	}

	logger.Info("running as the media user",
		slog.Int("uid", identity.UID),
		slog.Int("gid", identity.GID),
		slog.Any("took_ownership_of", outcome.Chowned),
		slog.String("note", "PUID/PGID set this. Volumes MediaNexus did not create "+
			"are left alone; mismatches are reported, not corrected"))
	return outcome, nil
}

// drop relinquishes root permanently.
//
// Order matters and is not interchangeable: supplementary groups and the gid
// must be surrendered while still privileged, because setuid is what removes the
// ability to make either call. Reversing it leaves the process in the target
// user's uid while retaining root's group memberships.
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

	// Verified rather than assumed. A silent failure here would run the whole
	// application as root while every log line claimed otherwise.
	if syscall.Geteuid() != identity.UID || syscall.Getuid() != identity.UID {
		return errors.New("privilege: still root after dropping; refusing to continue")
	}
	return nil
}

// chownTree gives one directory and its contents to identity.
//
// Only directories MediaNexus manages are passed in -- config, databases,
// backups, cache. The media library is deliberately never included: it holds
// the 500k files of ADR-22, it belongs to the user rather than to us, and
// rewriting its ownership at every boot would be both slow and presumptuous.
func chownTree(fsys fsx.FS, root string, identity Identity) (bool, error) {
	if _, err := fsys.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Created later by the section 14.3 checks, as the dropped user.
			return false, nil
		}
		return false, err
	}

	// The whole tree is walked every time, deliberately.
	//
	// Checking the top directory and skipping when it already matches is the
	// obvious optimisation and it is wrong: ownership of a directory says
	// nothing about what is inside it. A first run that failed after writing
	// config.yml as root leaves exactly that state -- /config owned by the
	// runtime user, config.yml owned by root -- and the shortcut would decide
	// there was nothing to do, then hand the process a configuration file it
	// cannot write. These directories hold tens of files, not the 500k of the
	// media library, so walking them costs nothing worth having.
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

// IdentityHint describes who the process is, for a permission error.
//
// Nearly every "permission denied" in a container is a PUID/PGID mismatch, and
// the message is useless without naming the identity that was refused. It lives
// here rather than beside either caller because there is one such sentence in
// the product, not two.
func IdentityHint() string {
	return fmt.Sprintf("running as uid=%d gid=%d; check PUID/PGID and the directory's owner",
		syscall.Getuid(), syscall.Getgid())
}

// WarnIfUnapplicable reports, before anything touches the filesystem, that the
// requested identity cannot be reached.
//
// It exists because of the order things fail in. When the container is started
// with --user as somebody who does not own /config, the first symptom is
// "permission denied" while writing config.yml -- which happens before the
// privilege drop is even attempted, and says nothing about the actual cause.
// The operator is then debugging a volume when the answer is a flag they
// passed.
//
// Writes to the bootstrap output rather than the logger, because the logger is
// configured from a file that has not been read yet.
func WarnIfUnapplicable(identity Identity, out io.Writer) {
	if syscall.Geteuid() == 0 || !identity.Requested {
		return
	}
	current := Identity{UID: syscall.Getuid(), GID: syscall.Getgid()}
	if current.UID == identity.UID && current.GID == identity.GID {
		return
	}

	// The write is not checked: this is a warning on the way to somewhere else,
	// and failing to print it is not a reason to stop.
	_, _ = fmt.Fprintf(out,
		"warning: PUID=%d PGID=%d were set, but this container is running as uid=%d gid=%d.\n"+
			"         Changing user needs root, and --user (or runAsUser) already took that\n"+
			"         away, so PUID and PGID cannot be applied and nothing will be chowned.\n"+
			"         Remove --user and let PUID/PGID do it, or set --user to match them.\n\n",
		identity.UID, identity.GID, current.UID, current.GID)
}
