package config_test

import (
	"io"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func memWith(t *testing.T, path, contents string) fsx.FS {
	t.Helper()
	mem := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := mem.MkdirAll("/run/secrets", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fsx.WriteAtomic(mem, path, func(w io.Writer) error { _, err := io.WriteString(w, contents); return err }); err != nil {
		t.Fatalf("write: %v", err)
	}
	return mem
}

func TestEnvFrom_AFileVariableSuppliesTheValueWithoutItsNewline(t *testing.T) {
	fsys := memWith(t, "/run/secrets/tmdb", "the-key\n")

	lookup, err := config.EnvFrom(fsys, []string{"SOLARR_TMDB_API_KEY_FILE=/run/secrets/tmdb"})
	if err != nil {
		t.Fatalf("EnvFrom: %v", err)
	}
	if got, found := lookup("SOLARR_TMDB_API_KEY"); !found || got != "the-key" {
		t.Errorf("lookup = %q, %v, want the-key", got, found)
	}
}

func TestEnvFrom_NamingBothFormsIsAnError(t *testing.T) {
	fsys := memWith(t, "/run/secrets/tmdb", "x")

	_, err := config.EnvFrom(fsys, []string{"SOLARR_PORT=1", "SOLARR_PORT_FILE=/run/secrets/tmdb"})
	if err == nil || !strings.Contains(err.Error(), "SOLARR_PORT") {
		t.Errorf("err = %v, want one naming SOLARR_PORT", err)
	}
}

func TestEnvFrom_AMissingFileIsAnErrorNamingTheVariable(t *testing.T) {
	_, err := config.EnvFrom(fsx.NewMem(clock.NewFakeAtEpoch()), []string{"SOLARR_PORT_FILE=/nope"})
	if err == nil || !strings.Contains(err.Error(), "SOLARR_PORT_FILE") {
		t.Errorf("err = %v, want one naming SOLARR_PORT_FILE", err)
	}
}

func TestEnvFrom_IgnoresFileVariablesOutsideTheNamespace(t *testing.T) {
	lookup, err := config.EnvFrom(fsx.NewMem(clock.NewFakeAtEpoch()), []string{"OTHER_FILE=/nope", "SOLARR_PORT=1"})
	if err != nil {
		t.Fatalf("EnvFrom: %v", err)
	}
	if got, _ := lookup("SOLARR_PORT"); got != "1" {
		t.Errorf("SOLARR_PORT = %q, want 1", got)
	}
}
