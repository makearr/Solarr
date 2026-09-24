package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"

	"github.com/robfig/cron/v3"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// scheduleCronParser matches internal/jobs' own cronParser (five fields:
// minute, hour, day-of-month, month, day-of-week) exactly, so a cadence
// accepted here is guaranteed to parse identically when internal/jobs.
// TaskRunner reads it back -- duplicated rather than imported, since
// internal/jobs and internal/db/settings share no dependency edge in either
// direction (architecture §2) and this is one five-field constant, not
// a third occurrence of a shared concept worth extracting yet.
var scheduleCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ADR-59: every registered task's own cadence and enabled flag live in
// settings.db (policy, ADR-25), keyed dynamically per task since the set of
// registered tasks is internal/jobs.TaskRegistry's own, not a fixed set
// settings.db enumerates.
func scheduleCadenceKey(taskKey string) string { return "schedule." + taskKey + ".cadence" }
func scheduleEnabledKey(taskKey string) string { return "schedule." + taskKey + ".enabled" }

// maxScheduleCadences bounds how many times of day one task may be given.
const maxScheduleCadences = 12

func scheduleStartupKey(taskKey string) string { return "schedule." + taskKey + ".run_at_startup" }

// ScheduleCadences answers a task's effective cron expressions: the stored override, or the
// default. A value stored before several were allowed is one bare expression.
func (s *Snapshot) ScheduleCadences(taskKey, defaultCadence string) []string {
	value, found := s.policy[scheduleCadenceKey(taskKey)]
	if !found || value == "" {
		return []string{defaultCadence}
	}
	if !strings.HasPrefix(value, "[") {
		return []string{value}
	}
	var cadences []string
	if err := json.Unmarshal([]byte(value), &cadences); err != nil || len(cadences) == 0 {
		return []string{defaultCadence}
	}
	return cadences
}

// ScheduleRunAtStartup answers whether a task fires once when Solarr starts; fallback is the
// task's own default.
func (s *Snapshot) ScheduleRunAtStartup(taskKey string, fallback bool) bool {
	return s.policyBool(scheduleStartupKey(taskKey), fallback)
}

// ScheduleEnabled answers whether a task should fire. Absent means true: a
// task nobody has touched runs on its default cadence.
func (s *Snapshot) ScheduleEnabled(taskKey string) bool {
	value, found := s.policy[scheduleEnabledKey(taskKey)]
	if !found {
		return true
	}
	return value != "false"
}

// SetScheduleCadences overrides one task's cron expressions (ADR-59, ADR-44: validated on
// write); the task fires at the earliest of them.
func (c *Cache) SetScheduleCadences(ctx context.Context, cl clock.Clock, taskKey string, cadences []string) error {
	if problems := validateScheduleCadences(cadences, cl.Now()); len(problems) > 0 {
		return problems
	}
	encoded, err := json.Marshal(cadences)
	if err != nil {
		return err
	}
	return c.setPolicy(ctx, cl, scheduleCadenceKey(taskKey), string(encoded))
}

// SetScheduleRunAtStartup sets whether a task fires once when Solarr starts.
func (c *Cache) SetScheduleRunAtStartup(ctx context.Context, cl clock.Clock, taskKey string, run bool) error {
	return c.setPolicy(ctx, cl, scheduleStartupKey(taskKey), strconv.FormatBool(run))
}

// SetScheduleEnabled enables or disables one task.
func (c *Cache) SetScheduleEnabled(ctx context.Context, cl clock.Clock, taskKey string, enabled bool) error {
	return c.setPolicy(ctx, cl, scheduleEnabledKey(taskKey), strconv.FormatBool(enabled))
}

func validateScheduleCadences(cadences []string, now time.Time) config.Problems {
	if len(cadences) == 0 || len(cadences) > maxScheduleCadences {
		return config.Problems{{Path: "cadences", Value: strconv.Itoa(len(cadences)),
			Expectation: fmt.Sprintf("between 1 and %d schedules", maxScheduleCadences)}}
	}
	var problems config.Problems
	seen := make(map[string]bool, len(cadences))
	for _, cadence := range cadences {
		if seen[cadence] {
			problems = append(problems, config.Problem{Path: "cadences", Value: cadence, Expectation: "each schedule once"})
			continue
		}
		seen[cadence] = true
		problems = append(problems, validateScheduleCadence(cadence, now)...)
	}
	return problems
}

func validateScheduleCadence(cadence string, now time.Time) config.Problems {
	parsed, err := scheduleCronParser.Parse(cadence)
	if err != nil {
		return config.Problems{{
			Path: "cadence", Value: cadence, Expectation: "a valid five-field cron expression",
		}}
	}
	if parsed.Next(now).IsZero() {
		return config.Problems{{
			Path: "cadence", Value: cadence,
			Expectation: "an expression that can actually fire, not one that parses but never matches (e.g. 30 February)",
		}}
	}
	return nil
}

// ScheduleEntry is one task's stored overrides in the settings export; a nil field was never changed.
type ScheduleEntry struct {
	Cadences     []string `yaml:"cadences,omitempty"`
	Enabled      *bool    `yaml:"enabled,omitempty"`
	RunAtStartup *bool    `yaml:"run_at_startup,omitempty"`
}

var scheduleTaskKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// scheduleOverrides collects every stored schedule.* policy by task; settings.db does not know
// the registered tasks, so it exports what was changed rather than every task.
func (s *Snapshot) scheduleOverrides() map[string]ScheduleEntry {
	overrides := make(map[string]ScheduleEntry)
	for key := range s.policy {
		rest, found := strings.CutPrefix(key, "schedule.")
		if !found {
			continue
		}
		taskKey, field, found := strings.Cut(rest, ".")
		if !found {
			continue
		}
		entry := overrides[taskKey]
		switch field {
		case "cadence":
			if s.policy[key] == "" {
				continue
			}
			entry.Cadences = s.ScheduleCadences(taskKey, "")
		case "enabled":
			enabled := s.ScheduleEnabled(taskKey)
			entry.Enabled = &enabled
		case "run_at_startup":
			run := s.ScheduleRunAtStartup(taskKey, false)
			entry.RunAtStartup = &run
		default:
			continue
		}
		overrides[taskKey] = entry
	}
	if len(overrides) == 0 {
		return nil
	}
	return overrides
}

// scheduleReference is a fixed instant to test "does this cadence ever fire" against, since a
// document is validated without a clock.
var scheduleReference = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func validateScheduleEntries(entries map[string]ScheduleEntry) config.Problems {
	var problems config.Problems
	for taskKey, entry := range entries {
		path := "schedules." + taskKey
		if !scheduleTaskKeyPattern.MatchString(taskKey) {
			problems = append(problems, config.Problem{Path: path, Value: taskKey,
				Expectation: "a task key: lower case letters, digits and underscores"})
			continue
		}
		if entry.Cadences == nil {
			continue
		}
		for _, problem := range validateScheduleCadences(entry.Cadences, scheduleReference) {
			problem.Path = path + "." + problem.Path
			problems = append(problems, problem)
		}
	}
	return problems
}

// importSchedules writes each entry's overrides; a task the document leaves out keeps its own.
func importSchedules(ctx context.Context, queries *settingsdb.Queries, cl clock.Clock,
	entries map[string]ScheduleEntry,
) error {
	at := cl.Now().Unix()
	for taskKey, entry := range entries {
		values := make(map[string]string, 3)
		if entry.Cadences != nil {
			encoded, err := json.Marshal(entry.Cadences)
			if err != nil {
				return fmt.Errorf("schedules.%s: %w", taskKey, err)
			}
			values[scheduleCadenceKey(taskKey)] = string(encoded)
		}
		if entry.Enabled != nil {
			values[scheduleEnabledKey(taskKey)] = strconv.FormatBool(*entry.Enabled)
		}
		if entry.RunAtStartup != nil {
			values[scheduleStartupKey(taskKey)] = strconv.FormatBool(*entry.RunAtStartup)
		}
		for key, value := range values {
			err := queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{Key: key, ValueJson: value, UpdatedAt: at})
			if err != nil {
				return fmt.Errorf("schedules.%s: %w", taskKey, err)
			}
		}
	}
	return nil
}
