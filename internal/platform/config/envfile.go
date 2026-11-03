package config

import (
	"fmt"
	"io"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// fileSuffix marks a variable whose value is read from the file it names, the Docker secrets convention.
const fileSuffix = "_FILE"

// maxEnvFileBytes bounds a secret file; the largest legitimate value is a key or a token.
const maxEnvFileBytes int64 = 64 << 10

// EnvFrom builds a Lookup over environ ("NAME=value" entries). A SOLARR_<NAME>_FILE entry
// supplies SOLARR_<NAME> from the file it names, minus one trailing newline; naming both is an error.
func EnvFrom(fsys fsx.FS, environ []string) (Lookup, error) {
	values := make(map[string]string, len(environ))
	for _, entry := range environ {
		if name, value, found := strings.Cut(entry, "="); found {
			values[name] = value
		}
	}

	fromFiles := make(map[string]string)
	for name, path := range values {
		if !strings.HasPrefix(name, EnvPrefix) || !strings.HasSuffix(name, fileSuffix) {
			continue
		}
		target := strings.TrimSuffix(name, fileSuffix)
		if _, direct := values[target]; direct {
			return nil, fmt.Errorf("config: %s and %s are both set; use one", target, name)
		}
		contents, err := readEnvFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("config: %s: %w", name, err)
		}
		fromFiles[target] = contents
	}
	for name, contents := range fromFiles {
		values[name] = contents
	}

	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}, nil
}

func readEnvFile(fsys fsx.FS, path string) (string, error) {
	file, err := fsys.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxEnvFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	if int64(len(data)) > maxEnvFileBytes {
		return "", fmt.Errorf("%s exceeds the %d byte limit", path, maxEnvFileBytes)
	}
	text := strings.TrimSuffix(string(data), "\n")
	return strings.TrimSuffix(text, "\r"), nil
}
