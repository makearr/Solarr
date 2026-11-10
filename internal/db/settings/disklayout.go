package settings

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// KeyDiskGroupOverrides holds the admin's corrections to the detected disk layout: path -> disk group label.
const KeyDiskGroupOverrides = "disk.group_overrides"

// DiskGroupOverrides is the stored corrections; anything unreadable is none.
func (s *Snapshot) DiskGroupOverrides() map[string]string {
	overrides := map[string]string{}
	if raw := s.policy[KeyDiskGroupOverrides]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
			return map[string]string{}
		}
	}
	return overrides
}

// SetDiskGroupOverrides replaces the corrections; a blank label drops the entry, so the detected group applies again.
func (c *Cache) SetDiskGroupOverrides(ctx context.Context, cl clock.Clock, overrides map[string]string) error {
	kept := make(map[string]string, len(overrides))
	for path, label := range overrides {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		if !strings.HasPrefix(path, "/") {
			return config.Problems{{Path: KeyDiskGroupOverrides, Value: path, Expectation: "an absolute path"}}
		}
		kept[path] = label
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return c.setPolicies(ctx, cl, map[string]string{KeyDiskGroupOverrides: string(encoded)})
}
