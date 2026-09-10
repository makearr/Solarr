package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// Duration is a time.Duration written the way a human writes one: 15s, 50ms, 90d.
type Duration time.Duration

// AsDuration returns the underlying duration.
func (d Duration) AsDuration() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML accepts a duration string. Go's parser has no day unit, which is
// the natural way to write a retention period, so days are expanded first.
func (d *Duration) UnmarshalYAML(raw []byte) error {
	var text string
	if err := yaml.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("expected a duration such as 15s or 90d")
	}
	parsed, err := parseDuration(text)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func parseDuration(text string) (time.Duration, error) {
	trimmed := strings.TrimSpace(text)
	if days, found := strings.CutSuffix(trimmed, "d"); found {
		count, err := strconv.ParseFloat(days, 64)
		if err == nil {
			return time.Duration(count * float64(24*time.Hour)), nil
		}
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("expected a duration such as 15s, 500ms or 90d, got %q", text)
	}
	return parsed, nil
}

// Bytes is a size written as 50GB, 512MB or a plain byte count.
type Bytes int64

// AsInt64 returns the size in bytes.
func (b Bytes) AsInt64() int64 { return int64(b) }

func (b Bytes) String() string {
	for _, unit := range byteUnits {
		if int64(b) >= unit.factor && int64(b)%unit.factor == 0 {
			return fmt.Sprintf("%d%s", int64(b)/unit.factor, unit.suffix)
		}
	}
	return strconv.FormatInt(int64(b), 10)
}

// Decimal units, not binary: a disk sold as 4TB holds 4×10¹² bytes, and a free
// space threshold that silently means something else is a support problem.
var byteUnits = []struct {
	suffix string
	factor int64
}{
	{"TB", 1000 * 1000 * 1000 * 1000},
	{"GB", 1000 * 1000 * 1000},
	{"MB", 1000 * 1000},
	{"KB", 1000},
}

// UnmarshalYAML accepts a size with an optional unit suffix.
func (b *Bytes) UnmarshalYAML(raw []byte) error {
	var text string
	if err := yaml.Unmarshal(raw, &text); err != nil {
		var number int64
		if numberErr := yaml.Unmarshal(raw, &number); numberErr != nil {
			return fmt.Errorf("expected a size such as 50GB or a byte count")
		}
		*b = Bytes(number)
		return nil
	}

	trimmed := strings.ToUpper(strings.TrimSpace(text))
	for _, unit := range byteUnits {
		digits, found := strings.CutSuffix(trimmed, unit.suffix)
		if !found {
			continue
		}
		count, err := strconv.ParseFloat(strings.TrimSpace(digits), 64)
		if err != nil {
			return fmt.Errorf("expected a size such as 50GB, got %q", text)
		}
		*b = Bytes(int64(count * float64(unit.factor)))
		return nil
	}

	count, err := strconv.ParseInt(strings.TrimSuffix(trimmed, "B"), 10, 64)
	if err != nil {
		return fmt.Errorf("expected a size such as 50GB or a byte count, got %q", text)
	}
	*b = Bytes(count)
	return nil
}

// Mode is the dry-run level (ADR-20).
type Mode string

const (
	// ModeNormal acts on decisions.
	ModeNormal Mode = "normal"
	// ModeObserve decides and records but never acts.
	ModeObserve Mode = "observe"
	// ModeShadow acts on everything except the irreversible steps.
	ModeShadow Mode = "shadow"
)

// AuthMode is how requests are authenticated (ADR-14).
type AuthMode string

const (
	// AuthLocal is username and password with argon2id hashing.
	AuthLocal AuthMode = "local"
	// AuthProxy trusts a header set by a reverse proxy.
	AuthProxy AuthMode = "proxy"
	// AuthAPIKeyOnly disables interactive login entirely.
	AuthAPIKeyOnly AuthMode = "apikey_only"
	// AuthNone serves every request as the administrator, with no credential of
	// any kind. Deliberate and file-only: nothing in the interface can set it,
	// because the one setting whose purpose is removing the protection around
	// the settings screen must not be reachable from inside it.
	AuthNone AuthMode = "none"
)

// LogLevel is the slog level.
type LogLevel string

// Levels accepted in config.yml.
const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
)

// LogFormat selects the slog handler.
type LogFormat string

// Formats accepted in config.yml.
const (
	FormatJSON LogFormat = "json"
	FormatText LogFormat = "text"
)

// allowedModes and friends drive both validation and the "expected one of"
// message, so the two can never disagree.
var (
	allowedModes     = []Mode{ModeNormal, ModeObserve, ModeShadow}
	allowedAuthModes = []AuthMode{AuthLocal, AuthProxy, AuthAPIKeyOnly, AuthNone}
	allowedLevels    = []LogLevel{LevelDebug, LevelInfo, LevelWarn, LevelError}
	allowedFormats   = []LogFormat{FormatJSON, FormatText}
)

func oneOf[T ~string](value T, allowed []T) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func listOf[T ~string](allowed []T) string {
	names := make([]string, len(allowed))
	for index, value := range allowed {
		names[index] = string(value)
	}
	return strings.Join(names, ", ")
}
