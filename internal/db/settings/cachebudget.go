package settings

import (
	"context"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// ADR-60's cache budget keys, the same instance_setting shape auth.required
// already uses.
const (
	KeyCacheMaxBytes      = "cache.max_bytes"
	KeyCacheMaxAgeDays    = "cache.max_age_days"
	KeyCacheImagesShare   = "cache.namespace_share.images"
	KeyCacheMetadataShare = "cache.namespace_share.metadata"
)

// ADR-60's own shipped defaults.
const (
	DefaultCacheMaxBytes             = 10 * 1024 * 1024 * 1024 // 10 GB
	DefaultCacheMaxAgeDays           = 90
	DefaultCacheImagesSharePercent   = 80
	DefaultCacheMetadataSharePercent = 20
)

// CacheBudget is ADR-60's own policy: a total byte ceiling, a per-entry age
// ceiling, and how the total splits between namespaces so images cannot
// starve metadata.
type CacheBudget struct {
	MaxBytes             int64
	MaxAgeDays           int64
	ImagesSharePercent   int64
	MetadataSharePercent int64
}

// CacheBudget reads the configured budget, or ADR-60's shipped defaults for
// any key nothing has stored yet.
func (s *Snapshot) CacheBudget() CacheBudget {
	return CacheBudget{
		MaxBytes:             s.policyInt(KeyCacheMaxBytes, DefaultCacheMaxBytes),
		MaxAgeDays:           s.policyInt(KeyCacheMaxAgeDays, DefaultCacheMaxAgeDays),
		ImagesSharePercent:   s.policyInt(KeyCacheImagesShare, DefaultCacheImagesSharePercent),
		MetadataSharePercent: s.policyInt(KeyCacheMetadataShare, DefaultCacheMetadataSharePercent),
	}
}

func (s *Snapshot) policyInt(key string, fallback int64) int64 {
	value, found := s.policy[key]
	if !found {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

// SetCacheBudget writes ADR-60's budget (ADR-44: validated on write). The
// two namespace shares must sum to 100 -- anything else means either some
// capacity is unaccounted for or double-counted, silently.
func (c *Cache) SetCacheBudget(ctx context.Context, cl clock.Clock, budget CacheBudget) error {
	if problems := validateCacheBudget(budget); len(problems) > 0 {
		return problems
	}
	for key, value := range map[string]int64{
		KeyCacheMaxBytes: budget.MaxBytes, KeyCacheMaxAgeDays: budget.MaxAgeDays,
		KeyCacheImagesShare: budget.ImagesSharePercent, KeyCacheMetadataShare: budget.MetadataSharePercent,
	} {
		if err := c.setPolicy(ctx, cl, key, strconv.FormatInt(value, 10)); err != nil {
			return err
		}
	}
	return nil
}

func validateCacheBudget(budget CacheBudget) config.Problems {
	var problems config.Problems
	if budget.MaxBytes <= 0 {
		problems = append(problems, config.Problem{
			Path: "max_bytes", Value: strconv.FormatInt(budget.MaxBytes, 10), Expectation: "a positive number of bytes",
		})
	}
	if budget.MaxAgeDays <= 0 {
		problems = append(problems, config.Problem{
			Path: "max_age_days", Value: strconv.FormatInt(budget.MaxAgeDays, 10), Expectation: "a positive number of days",
		})
	}
	if budget.ImagesSharePercent < 0 || budget.MetadataSharePercent < 0 {
		problems = append(problems, config.Problem{
			Path: "namespace_share", Expectation: "both shares must be non-negative",
		})
	}
	if budget.ImagesSharePercent+budget.MetadataSharePercent != 100 {
		problems = append(problems, config.Problem{
			Path: "namespace_share",
			Value: strconv.FormatInt(budget.ImagesSharePercent, 10) + "+" +
				strconv.FormatInt(budget.MetadataSharePercent, 10),
			Expectation: "the images and metadata shares must sum to 100",
		})
	}
	return problems
}

// ADR-60's own measured facts -- probed by internal/jobs/cachebudget's own
// scheduled task, never by a policy write, the same split
// root_folder.writable/total_bytes already keeps from read_only/
// min_free_bytes.
const (
	keyCacheImagesBytes   = "cache.measured.images_bytes"
	keyCacheMetadataBytes = "cache.measured.metadata_bytes"
	keyCacheMeasuredAt    = "cache.measured.at"
)

// CacheMeasurement is what the last measurement sweep found. found is false
// until the task has run at least once.
type CacheMeasurement struct {
	ImagesBytes   int64
	MetadataBytes int64
	MeasuredAt    int64
}

// CacheMeasurement reads the last measurement, or found=false if the task
// has never run.
func (s *Snapshot) CacheMeasurement() (CacheMeasurement, bool) {
	value, found := s.policy[keyCacheMeasuredAt]
	if !found {
		return CacheMeasurement{}, false
	}
	measuredAt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return CacheMeasurement{}, false
	}
	return CacheMeasurement{
		ImagesBytes: s.policyInt(keyCacheImagesBytes, 0), MetadataBytes: s.policyInt(keyCacheMetadataBytes, 0),
		MeasuredAt: measuredAt,
	}, true
}

// SetCacheMeasurement records what a measurement sweep found. Not validated
// (ADR-44 governs user input, not a probed fact) -- the same reasoning
// SetRootFolderCapacity's own doc comment gives.
func (c *Cache) SetCacheMeasurement(ctx context.Context, cl clock.Clock, measurement CacheMeasurement) error {
	for key, value := range map[string]int64{
		keyCacheImagesBytes: measurement.ImagesBytes, keyCacheMetadataBytes: measurement.MetadataBytes,
		keyCacheMeasuredAt: measurement.MeasuredAt,
	} {
		if err := c.setPolicy(ctx, cl, key, strconv.FormatInt(value, 10)); err != nil {
			return err
		}
	}
	return nil
}
