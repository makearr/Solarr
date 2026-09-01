package config

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"strconv"

	"github.com/robfig/cron/v3"
)

// minimumBackupsKept is ADR-15's floor. Fewer than three retained backups means
// a corruption discovered late has nothing clean to fall back to.
const minimumBackupsKept = 3

// cronParser matches what the backup scheduler will use, so a schedule accepted
// at startup cannot be rejected later by the thing that runs it.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// validate collects every value and cross-field fault. It never returns early:
// fixing configuration one error per restart is the experience ADR-44 exists to
// prevent.
func (c Config) validate() Problems {
	var problems Problems

	problems = append(problems, c.validateVersion()...)
	problems = append(problems, c.Server.validate()...)
	problems = append(problems, c.Paths.validate()...)
	problems = append(problems, c.Security.validate()...)
	problems = append(problems, c.Database.validate()...)
	problems = append(problems, c.Backup.validate()...)
	problems = append(problems, c.Retention.validate()...)
	problems = append(problems, c.Storage.validate()...)
	problems = append(problems, c.Workers.validate()...)
	problems = append(problems, c.Logging.validate()...)

	if !oneOf(c.Mode, allowedModes) {
		problems = append(problems, newProblem("mode", string(c.Mode),
			"expected one of: "+listOf(allowedModes)))
	}
	return problems
}

func (c Config) validateVersion() Problems {
	switch {
	case c.Version <= 0:
		return Problems{newProblem("version", strconv.Itoa(c.Version),
			fmt.Sprintf("expected the config schema version, currently %d", CurrentVersion))}
	case c.Version > CurrentVersion:
		// Downgrading would silently discard settings this binary cannot
		// represent, so a newer file is a hard failure (§14.2).
		return Problems{newProblem("version", strconv.Itoa(c.Version),
			fmt.Sprintf("written by a newer MediaNexus; this binary understands up to %d", CurrentVersion))}
	}
	return nil
}

func (s Server) validate() Problems {
	var problems Problems

	if s.Port < 1 || s.Port > 65535 {
		problems = append(problems, newProblem("server.port", strconv.Itoa(s.Port),
			"out of range (1–65535)"))
	}
	if s.Bind != "" {
		if _, err := netip.ParseAddr(s.Bind); err != nil {
			problems = append(problems, newProblem("server.bind", s.Bind,
				"expected an IP address to listen on, such as 0.0.0.0 or ::"))
		}
	}
	if s.BaseURL == "" || s.BaseURL[0] != '/' {
		problems = append(problems, newProblem("server.base_url", s.BaseURL,
			"expected a path beginning with /"))
	}
	return problems
}

func (p Paths) validate() Problems {
	var problems Problems
	for _, entry := range []struct{ path, value string }{
		{"paths.config_dir", p.ConfigDir},
		{"paths.data_dir", p.DataDir},
		{"paths.db_dir", p.DBDir},
		{"paths.cache_dir", p.CacheDir},
		{"paths.backup_dir", p.BackupDir},
	} {
		if entry.value == "" {
			problems = append(problems, newProblem(entry.path, "", "expected an absolute path"))
			continue
		}
		if !filepath.IsAbs(entry.value) {
			problems = append(problems, newProblem(entry.path, entry.value,
				"expected an absolute path"))
		}
	}
	return problems
}

func (s Security) validate() Problems {
	var problems Problems

	if !oneOf(s.AuthMode, allowedAuthModes) {
		problems = append(problems, newProblem("security.auth_mode", string(s.AuthMode),
			"expected one of: "+listOf(allowedAuthModes)))
	}

	// Trusting a header without knowing who may set it means anyone can claim to
	// be any user (§14.1's cross-field example).
	if s.AuthMode == AuthProxy {
		if len(s.TrustedProxyCIDRs) == 0 {
			problems = append(problems, newProblem("security.auth_mode", string(s.AuthMode),
				"requires `security.trusted_proxy_cidrs`"))
		}
		if s.TrustedProxyHeader == "" {
			problems = append(problems, newProblem("security.auth_mode", string(s.AuthMode),
				"requires `security.trusted_proxy_header`"))
		}
	}

	for index, raw := range s.TrustedProxyCIDRs {
		if _, err := netip.ParsePrefix(raw); err != nil {
			problems = append(problems, newProblem(
				fmt.Sprintf("security.trusted_proxy_cidrs[%d]", index), raw,
				"expected a CIDR block such as 10.0.0.0/8"))
		}
	}
	return problems
}

func (d Database) validate() Problems {
	var problems Problems

	if !oneOf(d.Driver, allowedDrivers) {
		problems = append(problems, newProblem("database.driver", string(d.Driver),
			"expected one of: "+listOf(allowedDrivers)+" (others need CGO, which ADR-30 forbids)"))
	}
	if d.BusyTimeout <= 0 {
		problems = append(problems, newProblem("database.busy_timeout", d.BusyTimeout.String(),
			"expected a positive duration"))
	}
	if !isPowerOfTwo(d.PageSize) || d.PageSize < 512 || d.PageSize > 65536 {
		problems = append(problems, newProblem("database.page_size", strconv.Itoa(d.PageSize),
			"expected a power of two between 512 and 65536"))
	}
	if d.CacheSizeKB <= 0 {
		problems = append(problems, newProblem("database.cache_size_kb", strconv.Itoa(d.CacheSizeKB),
			"expected a positive size in kilobytes"))
	}
	if d.WriteWarn <= 0 {
		problems = append(problems, newProblem("database.write_warn", d.WriteWarn.String(),
			"expected a positive duration"))
	}
	if d.WriteHard <= 0 {
		problems = append(problems, newProblem("database.write_hard", d.WriteHard.String(),
			"expected a positive duration"))
	}
	// A hard limit below the warning threshold would fail transactions that
	// never even warned, which reads as a bug rather than a budget.
	if d.WriteWarn > 0 && d.WriteHard > 0 && d.WriteHard <= d.WriteWarn {
		problems = append(problems, newProblem("database.write_hard", d.WriteHard.String(),
			fmt.Sprintf("expected a duration above `database.write_warn` (%s)", d.WriteWarn)))
	}
	return problems
}

func (b Backup) validate() Problems {
	var problems Problems

	if b.Schedule != "" {
		if _, err := cronParser.Parse(b.Schedule); err != nil {
			problems = append(problems, newProblem("backup.schedule", b.Schedule,
				"expected a five-field cron expression such as \"0 3 * * *\""))
		}
	}
	if b.Keep < minimumBackupsKept {
		problems = append(problems, newProblem("backup.keep", strconv.Itoa(b.Keep),
			fmt.Sprintf("expected at least %d (ADR-15)", minimumBackupsKept)))
	}
	if b.KeepSettings < minimumBackupsKept {
		problems = append(problems, newProblem("backup.keep_settings", strconv.Itoa(b.KeepSettings),
			fmt.Sprintf("expected at least %d (ADR-15)", minimumBackupsKept)))
	}
	if !b.BeforeMigration {
		// Migrations are forward-only, so the pre-migration backup is the only
		// rollback there is (Invariant 7).
		problems = append(problems, newProblem("backup.before_migration", "false",
			"cannot be disabled: it is the only rollback a forward-only migration has"))
	}
	return problems
}

func (r Retention) validate() Problems {
	var problems Problems

	if r.DecisionsFull <= 0 {
		problems = append(problems, newProblem("retention.decisions_full", r.DecisionsFull.String(),
			"expected a positive duration such as 90d"))
	}
	if r.Candidates <= 0 {
		problems = append(problems, newProblem("retention.candidates", r.Candidates.String(),
			"expected a positive duration such as 168h"))
	}
	// Keeping a candidate longer than the decision explaining it leaves the
	// flagship "why wasn't this downloaded?" answer dangling (F1).
	if r.Candidates > 0 && r.DecisionsFull > 0 && r.Candidates > r.DecisionsFull {
		problems = append(problems, newProblem("retention.candidates", r.Candidates.String(),
			fmt.Sprintf("expected no longer than `retention.decisions_full` (%s)", r.DecisionsFull)))
	}
	return problems
}

func (s Storage) validate() Problems {
	var problems Problems

	if s.MinFreeBytes < 0 {
		problems = append(problems, newProblem("storage.min_free_bytes", s.MinFreeBytes.String(),
			"expected a size such as 50GB"))
	}
	if s.MinFreePercent < 0 || s.MinFreePercent > 100 {
		problems = append(problems, newProblem("storage.min_free_percent", strconv.Itoa(s.MinFreePercent),
			"out of range (0–100)"))
	}
	return problems
}

func (w Workers) validate() Problems {
	var problems Problems

	for _, entry := range []struct {
		path  string
		value int
	}{
		{"workers.search", w.Search},
		{"workers.acquire", w.Acquire},
		{"workers.metadata", w.Metadata},
	} {
		if entry.value < 1 {
			problems = append(problems, newProblem(entry.path, strconv.Itoa(entry.value),
				"expected at least 1"))
		}
	}

	// Import concurrency is fixed at 1 and is not configurable (ADR-3). Saying
	// so is better than accepting a value and ignoring it.
	if w.Import != 1 {
		problems = append(problems, newProblem("workers.import", strconv.Itoa(w.Import),
			"fixed at 1 and not configurable (ADR-3)"))
	}
	return problems
}

func (l Logging) validate() Problems {
	var problems Problems

	if !oneOf(l.Level, allowedLevels) {
		problems = append(problems, newProblem("logging.level", string(l.Level),
			"expected one of: "+listOf(allowedLevels)))
	}
	if !oneOf(l.Format, allowedFormats) {
		problems = append(problems, newProblem("logging.format", string(l.Format),
			"expected one of: "+listOf(allowedFormats)))
	}
	return problems
}

func isPowerOfTwo(value int) bool {
	return value > 0 && value&(value-1) == 0
}
