package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

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
