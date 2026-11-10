package settings

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Keys of the scan schedule.
const (
	// KeyScanIntervalHours is the person's own time between scans of a root; 0 lets Solarr decide by what it can see.
	KeyScanIntervalHours = "scan.interval_hours"
	// KeyScanFingerprints is each root's used space and top-level entry count at its last scan, to tell whether it changed.
	KeyScanFingerprints = "scan.fingerprints"
)

// maxScanIntervalHours is a year; longer would be a typo.
const maxScanIntervalHours = 24 * 365

// ScanInterval is the person's chosen time between scans; zero means decide automatically.
func (s *Snapshot) ScanInterval() time.Duration {
	return time.Duration(s.policyInt(KeyScanIntervalHours, 0)) * time.Hour
}

// SetScanIntervalHours stores the chosen interval; 0 is automatic.
func (c *Cache) SetScanIntervalHours(ctx context.Context, cl clock.Clock, hours int64) error {
	if hours < 0 || hours > maxScanIntervalHours {
		return config.Problems{{Path: KeyScanIntervalHours, Value: strconv.FormatInt(hours, 10), Expectation: "0 (automatic) to 8760 hours"}}
	}
	return c.setPolicies(ctx, cl, map[string]string{KeyScanIntervalHours: strconv.FormatInt(hours, 10)})
}

// ScanFingerprints is what each root looked like at its last scan; unreadable is none.
func (s *Snapshot) ScanFingerprints() map[int64]string {
	fingerprints := map[int64]string{}
	if raw := s.policy[KeyScanFingerprints]; raw != "" {
		if json.Unmarshal([]byte(raw), &fingerprints) != nil {
			return map[int64]string{}
		}
	}
	return fingerprints
}

// SetScanFingerprint records what a root looks like as it is scanned.
func (c *Cache) SetScanFingerprint(ctx context.Context, cl clock.Clock, rootFolderID int64, fingerprint string) error {
	all := c.Snapshot().ScanFingerprints()
	if all[rootFolderID] == fingerprint {
		return nil
	}
	all[rootFolderID] = fingerprint
	encoded, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return c.setPolicies(ctx, cl, map[string]string{KeyScanFingerprints: string(encoded)})
}
