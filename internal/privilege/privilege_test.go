package privilege_test

import (
	"io"
	"log/slog"
	"strings"
	"syscall"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/privilege"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		want        privilege.Identity
		wantError   string
	}{
		{
			// Requested stays false: nobody asked for 1000, it is simply what
			// happens in its absence.
			name:        "unset falls back to the image's media user",
			environment: nil,
			want:        privilege.Identity{UID: 1000, GID: 1000, Requested: false},
		},
		{
			name:        "both set",
			environment: map[string]string{"PUID": "1027", "PGID": "100"},
			want:        privilege.Identity{UID: 1027, GID: 100, Requested: true},
		},
		{
			// Setting one governs the identity as a whole: the other half takes
			// the default, and the pair is still a request.
			name:        "one set, one defaulted, is still a request",
			environment: map[string]string{"PGID": "100"},
			want:        privilege.Identity{UID: 1000, GID: 100, Requested: true},
		},
		{
			// Asking for exactly the default is still asking, and has to be
			// distinguishable -- it is the difference between a request that
			// cannot be honoured and no request at all.
			name:        "asking for the default is still a request",
			environment: map[string]string{"PUID": "1000", "PGID": "1000"},
			want:        privilege.Identity{UID: 1000, GID: 1000, Requested: true},
		},
		{
			name:        "empty is treated as unset, not as zero",
			environment: map[string]string{"PUID": ""},
			want:        privilege.Identity{UID: 1000, GID: 1000, Requested: false},
		},
		{
			// Silently falling back to 1000 would surface much later as a
			// permission error against the volume, sending the operator to
			// look at the mount rather than at the typo.
			name:        "a typo is an error, not a fallback",
			environment: map[string]string{"PUID": "1O00"},
			wantError:   "must be a number",
		},
		{
			name:        "root is refused",
			environment: map[string]string{"PUID": "0"},
			wantError:   "greater than 0",
		},
		{
			name:        "negative is refused",
			environment: map[string]string{"PGID": "-5"},
			wantError:   "greater than 0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(key string) (string, bool) {
				value, found := test.environment[key]
				return value, found
			}

			got, err := privilege.Resolve(lookup)
			if test.wantError != "" {
				if err == nil {
					t.Fatalf("Resolve succeeded with %v, want an error", got)
				}
				if !strings.Contains(err.Error(), test.wantError) {
					t.Errorf("error = %q, want it to mention %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != test.want {
				t.Errorf("Resolve = %+v, want %+v", got, test.want)
			}
		})
	}
}

// Apply is a no-op for an unprivileged process, which is what the test runner
// is. That is the branch taken whenever an operator passes --user, so it is
// worth asserting rather than assuming.
func TestApply_LeavesAnUnprivilegedProcessAlone(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/config", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	outcome, err := privilege.Apply(fsys, privilege.Identity{UID: 1, GID: 1},
		[]string{"/config"}, discardLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if outcome.Dropped {
		t.Error("reported dropping privileges it never had")
	}
	if len(outcome.Chowned) != 0 {
		t.Errorf("chowned %v; an unprivileged process must not try", outcome.Chowned)
	}

	// Ownership must be untouched: a process that cannot chown must not pretend
	// it did, or the caller believes the volume is ready when it is not.
	info, err := fsys.Stat("/config")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.UID != -1 || info.GID != -1 {
		t.Errorf("ownership changed to %d:%d", info.UID, info.GID)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The warning has to reach the operator before the failure it explains.
//
// Started with a --user that does not own /config, the first thing that breaks
// is writing config.yml, and "permission denied" sends someone to look at the
// volume when the answer is a flag they passed.
func TestWarnIfUnapplicable(t *testing.T) {
	current := privilege.Identity{UID: syscall.Getuid(), GID: syscall.Getgid()}

	tests := []struct {
		name     string
		identity privilege.Identity
		wantWarn bool
	}{
		{
			name:     "a request this process cannot honour is warned about",
			identity: privilege.Identity{UID: current.UID + 7, GID: current.GID, Requested: true},
			wantWarn: true,
		},
		{
			name:     "a request that already matches is silent",
			identity: privilege.Identity{UID: current.UID, GID: current.GID, Requested: true},
			wantWarn: false,
		},
		{
			// Nobody asked for 1000, so there is no request to disappoint --
			// warning here would fire on every ordinary --user deployment.
			name:     "the default is not a request, so it is silent",
			identity: privilege.Identity{UID: current.UID + 7, GID: current.GID, Requested: false},
			wantWarn: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if syscall.Geteuid() == 0 {
				t.Skip("meaningless as root, which can always honour the request")
			}

			var out strings.Builder
			privilege.WarnIfUnapplicable(test.identity, &out)

			warned := out.Len() > 0
			if warned != test.wantWarn {
				t.Errorf("warned = %v, want %v (output: %q)", warned, test.wantWarn, out.String())
			}
			if test.wantWarn && !strings.Contains(out.String(), "--user") {
				t.Errorf("the warning should name the cause: %q", out.String())
			}
		})
	}
}
