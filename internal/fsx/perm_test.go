package fsx

import (
	"io/fs"
	"testing"
)

// These two constants are a decision, not a detail, and the decision is easy to
// get backwards in either direction. The mode a file lands with also depends on
// the process umask, so this asserts the constants themselves: it is the only
// check of them that cannot be turned green or red by the environment.
func TestFileModeConstants(t *testing.T) {
	tests := []struct {
		name string
		got  fs.FileMode
		want fs.FileMode
		why  string
	}{
		{
			name: "filePerm",
			got:  filePerm,
			want: 0o644,
			why: "imported media must stay readable by the media server, which runs " +
				"as a different user. Tightening this breaks the library rather than " +
				"protecting anything -- secrets have their own mode",
		},
		{
			name: "secretFilePerm",
			got:  secretFilePerm,
			want: 0o600,
			why: "config.yml holds the only copy of the encryption key (ADR-14). " +
				"Widening this makes the encryption of settings.db decorative",
		},
	}

	for _, test := range tests {
		if test.got != test.want {
			t.Errorf("%s is %04o, want %04o: %s", test.name, test.got, test.want, test.why)
		}
	}

	if secretFilePerm&0o077 != 0 {
		t.Errorf("secretFilePerm %04o grants access beyond the owner", secretFilePerm)
	}
	if filePerm&0o044 == 0 {
		t.Errorf("filePerm %04o is not readable by the media server", filePerm)
	}
}
