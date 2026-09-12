package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
)

// SetServerPort surgically updates only server.port in an existing config.yml
// document, preserving every other byte -- comments, ordering, blank lines.
// config.yml is a hand-edited bootstrap file by design (create.go's own
// template says so); a full struct remarshal would silently strip every
// comment in it, which is not an acceptable price for writing back one
// field (ADR-58's revised precedence: config.yml now outranks settings.db,
// and the interface may write to it).
func SetServerPort(data []byte, port int) ([]byte, error) {
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}

	path, err := yaml.PathString("$.server.port")
	if err != nil {
		return nil, fmt.Errorf("config: build path: %w", err)
	}
	if err := path.ReplaceWithReader(file, strings.NewReader(strconv.Itoa(port))); err != nil {
		return nil, fmt.Errorf("config: set server.port: %w", err)
	}

	result := []byte(file.String())
	// ReplaceWithReader answers no error and silently changes nothing when
	// the path does not exist in the document (proven live: a document with
	// no server section round-trips untouched) -- verified explicitly
	// rather than trusted, since a loud failure here beats a config.yml
	// that silently never got the port a caller just asked to set.
	reparsed, err := Parse(result)
	if err != nil {
		return nil, fmt.Errorf("config: patched document is invalid: %w", err)
	}
	if reparsed.Server.Port != port {
		return nil, fmt.Errorf("config: server.port not found in the document -- expected to write %d", port)
	}

	return result, nil
}
