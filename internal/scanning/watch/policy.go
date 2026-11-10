package watch

import "time"

// Intervals between scans of a root, by what Solarr can tell about it changing without reading the disk.
const (
	// WatchedInterval is for a root whose work folders are watched: events carry the changes, the scan only verifies.
	WatchedInterval = 7 * 24 * time.Hour
	// UnchangedInterval is for a root with no watcher whose size and entry count are the same as at the last scan.
	UnchangedInterval = 24 * time.Hour
	// BlindInterval is for a root nothing can be learned about cheaply.
	BlindInterval = 6 * time.Hour
	// StartupAge is how old the last scan must be for Solarr to scan at start.
	StartupAge = 12 * time.Hour
	// WorkFolderDepth is the level of the artist, series or movie folders under a root.
	WorkFolderDepth = 1
)

// Fingerprint says whether a cheap look at the root (used space and top-level entry count) differs from the last scan.
type Fingerprint int

// The answers.
const (
	FingerprintUnknown Fingerprint = iota
	FingerprintSame
	FingerprintChanged
)

// Why is the reason a scan is, or is not, due; a code, so a screen can word it.
type Why string

// The reasons.
const (
	WhyManual         Why = "manual"
	WhyNeverScanned   Why = "never_scanned"
	WhyStartupStale   Why = "startup_stale"
	WhyStartupRecent  Why = "startup_recent"
	WhyWatcherLost    Why = "watcher_lost"
	WhyIntervalPassed Why = "interval_passed"
	WhyWithinInterval Why = "within_interval"
	WhyChangedSize    Why = "size_or_count_changed"
	WhyUnchanged      Why = "size_and_count_unchanged"
)

// Input is what the decision rests on.
type Input struct {
	Now      time.Time
	LastScan time.Time // when the last scan finished; zero if never
	// Startup is the run right after the process began; Manual is a person asking.
	Startup, Manual bool
	// Watching is true when a watcher has been alive since LastScan without losing events; CoveredDepth is the
	// deepest level it covers completely.
	Watching     bool
	CoveredDepth int
	Fingerprint  Fingerprint
	// Interval is the person's own choice; zero means decide by what is known.
	Interval time.Duration
}

// ScanDue says whether a root should be scanned now.
func ScanDue(in Input) (due bool, why Why) {
	age := in.Now.Sub(in.LastScan)
	switch {
	case in.Manual:
		return true, WhyManual
	case in.LastScan.IsZero():
		return true, WhyNeverScanned
	case in.Startup:
		if age > StartupAge {
			return true, WhyStartupStale
		}
		return false, WhyStartupRecent
	case in.Interval > 0:
		return age >= in.Interval, intervalWhy(age >= in.Interval)
	case in.Watching && in.CoveredDepth >= WorkFolderDepth:
		return age >= WatchedInterval, intervalWhy(age >= WatchedInterval)
	case in.Fingerprint == FingerprintSame:
		if age >= UnchangedInterval {
			return true, WhyIntervalPassed
		}
		return false, WhyUnchanged
	case in.Fingerprint == FingerprintChanged:
		return true, WhyChangedSize
	default:
		return age >= BlindInterval, intervalWhy(age >= BlindInterval)
	}
}

func intervalWhy(due bool) Why {
	if due {
		return WhyIntervalPassed
	}
	return WhyWithinInterval
}
