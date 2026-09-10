// Package score is ADR-40's quality/language interpreter: hard gates, then
// lexicographic tiers, then an additive score, producing a model.Decision. Pure
// rule evaluation, no I/O -- a caller decodes a settings.QualityProfile's raw JSON
// and passes the parsed rules in.
package score
