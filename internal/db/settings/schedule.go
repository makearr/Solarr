package settings

import (
	"context"
	"strconv"
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

// ScheduleCadence answers a task's effective cron expression: a stored
// override if one exists, defaultCadence otherwise. An absent override means
// "use the registry's own default", not "invalid" -- the same reasoning
// ImportAutoCommitConfidence's own doc comment gives.
func (s *Snapshot) ScheduleCadence(taskKey, defaultCadence string) string {
	if value, found := s.policy[scheduleCadenceKey(taskKey)]; found {
		return value
	}
	return defaultCadence
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

// SetScheduleCadence overrides one task's cadence (ADR-59, ADR-44: validated
// on write). Rejects both a cron expression that fails to parse and one
// that parses but can never fire -- a nonexistent calendar date like the
// 30du of February -- since either would otherwise be accepted and then
// silently never run.
func (c *Cache) SetScheduleCadence(ctx context.Context, cl clock.Clock, taskKey, cadence string) error {
	if problems := validateScheduleCadence(cadence, cl.Now()); len(problems) > 0 {
		return problems
	}
	return c.setPolicy(ctx, cl, scheduleCadenceKey(taskKey), cadence)
}

// SetScheduleEnabled enables or disables one task.
func (c *Cache) SetScheduleEnabled(ctx context.Context, cl clock.Clock, taskKey string, enabled bool) error {
	return c.setPolicy(ctx, cl, scheduleEnabledKey(taskKey), strconv.FormatBool(enabled))
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
