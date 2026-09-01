package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/goccy/go-yaml"

	"github.com/makearr/MediaNexus/internal/fsx"
)

// ErrNotFound reports a missing config.yml. It is not itself a failure: a first
// run writes one from the defaults.
var ErrNotFound = errors.New("config: file not found")

// Parse decodes and validates a config document, returning every problem it
// finds rather than the first (ADR-44).
//
// It runs three passes over the same document because they fail differently.
// Unknown keys are found structurally, so all of them are reported. Type
// mismatches come from the decoder, which stops at the first. Value and
// cross-field rules are checked afterwards and are all reported. Merging them
// means a document with a typo, a bad range and a contradiction reports all
// three in one go.
func Parse(data []byte) (Config, error) {
	config := Default()

	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return config, Problems{{
			Path:        "(document)",
			Value:       "",
			Expectation: fmt.Sprintf("valid YAML: %v", err),
		}}
	}

	problems := unknownKeys(document)

	// A file with no version key is the first schema, not the current one.
	// Defaulting it to CurrentVersion would make an old document skip every
	// migration between the two the moment CurrentVersion moves on (§14.2).
	if _, versioned := document["version"]; !versioned {
		config.Version = 1
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		problems = append(problems, Problem{
			Path:        "(document)",
			Value:       "",
			Expectation: fmt.Sprintf("values matching their types: %v", err),
		})
		// Decoding stopped, so the partially filled config is not worth
		// validating — its untouched fields would report defaults as faults.
		return config, problems
	}

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
	return Parse(data)
}
