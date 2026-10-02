// Package judge is what every media type's candidate search shares: asking all indexers at once, judging a release by
// a library's ordered quality profiles, ranking the finds and recording the decisions. A media type supplies only how
// a release name maps to the thing wanted (stage 8 T18/T19; music's own copy in internal/media/music predates it).
package judge
