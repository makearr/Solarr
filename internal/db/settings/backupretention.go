package settings

import (
	"context"
	"strconv"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Backup retention moved from config.yml into the scheduler's cleanup task (answered 2026-09-24).
const (
	KeyBackupRetention = "backup.retention"
	KeyBackupKeepCount = "backup.keep_count"
	KeyBackupKeepDays  = "backup.keep_days"
)

// BackupRetentionMode is how the cleanup task decides what is old.
type BackupRetentionMode string

const (
	// BackupKeepCount keeps the newest KeepCount backups of each database.
	BackupKeepCount BackupRetentionMode = "count"
	// BackupKeepDays keeps what is younger than KeepDays, and always the newest three (ADR-15).
	BackupKeepDays BackupRetentionMode = "days"
)

// Retention defaults and ADR-15's floor, which no setting can go below.
const (
	DefaultBackupKeepCount = 7
	DefaultBackupKeepDays  = 30
	MinimumBackupsKept     = 3
)

// BackupRetention is the cleanup task's rule.
type BackupRetention struct {
	Mode      BackupRetentionMode
	KeepCount int64
	KeepDays  int64
}

// BackupRetention reads the configured rule.
func (s *Snapshot) BackupRetention() BackupRetention {
	mode := BackupRetentionMode(s.policy[KeyBackupRetention])
	if mode != BackupKeepDays {
		mode = BackupKeepCount
	}
	return BackupRetention{
		Mode:      mode,
		KeepCount: s.policyInt(KeyBackupKeepCount, DefaultBackupKeepCount),
		KeepDays:  s.policyInt(KeyBackupKeepDays, DefaultBackupKeepDays),
	}
}

func validateBackupRetention(retention BackupRetention) config.Problems {
	var problems config.Problems
	if retention.Mode != BackupKeepCount && retention.Mode != BackupKeepDays {
		problems = append(problems, config.Problem{Path: KeyBackupRetention, Value: string(retention.Mode),
			Expectation: "count or days"})
	}
	if retention.KeepCount < MinimumBackupsKept || retention.KeepCount > 1000 {
		problems = append(problems, config.Problem{Path: KeyBackupKeepCount,
			Value: strconv.FormatInt(retention.KeepCount, 10), Expectation: "3 to 1000: at least 3 backups are always kept"})
	}
	if retention.KeepDays < 1 || retention.KeepDays > 3650 {
		problems = append(problems, config.Problem{Path: KeyBackupKeepDays,
			Value: strconv.FormatInt(retention.KeepDays, 10), Expectation: "1 to 3650 days"})
	}
	return problems
}

// SetBackupRetention writes the cleanup rule (ADR-44: validated on write).
func (c *Cache) SetBackupRetention(ctx context.Context, cl clock.Clock, retention BackupRetention) error {
	if problems := validateBackupRetention(retention); len(problems) > 0 {
		return problems
	}
	values := map[string]string{
		KeyBackupRetention: string(retention.Mode),
		KeyBackupKeepCount: strconv.FormatInt(retention.KeepCount, 10),
		KeyBackupKeepDays:  strconv.FormatInt(retention.KeepDays, 10),
	}
	// One transaction, so the rule never changes halfway and settings.db is backed up once.
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		for key, value := range values {
			err := queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
				Key: key, ValueJson: value, UpdatedAt: cl.Now().Unix(),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
}
