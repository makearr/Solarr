package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/goccy/go-yaml"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ErrNotFound reports a missing config.yml. It is not itself a failure: a first
// run writes one from the defaults.
var ErrNotFound = errors.New("config: file not found")

// Parse decodes and validates a config document, returning every problem it finds rather than
// the first (ADR-44).
func Parse(data []byte) (Config, error) {
	return ParseWithEnv(data, func(string) (string, bool) { return "", false })
}

// ParseWithEnv is Parse with environment overrides applied between decoding and validation, so
// an override is checked by exactly the same rules as a file value (ADR-44).
func ParseWithEnv(data []byte, lookup Lookup) (Config, error) {
	config := Default()

	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return config, Problems{{
			Path:        "(document)",
			Value:       "",
			Expectation: "valid YAML: " + sanitiseErrorText(err.Error()),
		}}
	}

	problems := unknownKeys(document)

	// A file with no version key is the first schema, not the current one (§14.2).
	if _, versioned := document["version"]; !versioned {
		config.Version = 1
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		problems = append(problems, Problem{
			Path:        "(document)",
			Value:       "",
			Expectation: "values matching their types: " + sanitiseErrorText(err.Error()),
		})
		// Decoding stopped, so the partially filled config is not worth
		// validating — its untouched fields would report defaults as faults.
		return config, problems
	}

	problems = append(problems, applyEnv(&config, lookup)...)
	problems = append(problems, config.validate()...)
	return config, problems.OrNil()
}

// Load reads and parses config.yml. A missing file is ErrNotFound, so the caller
// can decide between writing defaults on a first run and failing.
func Load(fsys fsx.FS, path string) (Config, error) {
	file, err := fsys.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return Default(), fmt.Errorf("config: open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		return Default(), fmt.Errorf("config: read %s: %w", path, err)
	}
	return ParseWithEnv(data, os.LookupEnv)
}

// OSEnv reads the process environment.
func OSEnv() Lookup { return os.LookupEnv }
