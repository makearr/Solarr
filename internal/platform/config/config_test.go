package config_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/platform/config"
)

func TestParse_DefaultsAreValidOnTheirOwn(t *testing.T) {
	// Every setting has a default and works untouched (Invariant 38). An empty
	// document must therefore be accepted.
	parsed, err := config.Parse([]byte("version: 1\n"))
	if err != nil {
		t.Fatalf("Parse of a minimal document = %v, want nil", err)
	}
	if parsed.Server.Port != 5454 {
		t.Errorf("Server.Port = %d, want the default 5454", parsed.Server.Port)
	}
	if parsed.Workers.Import != 1 {
		t.Errorf("Workers.Import = %d, want the fixed 1", parsed.Workers.Import)
	}
}

func TestParse_AcceptsAFullDocument(t *testing.T) {
	document := `
version: 1
server:
  bind: 127.0.0.1
  port: 9000
  base_url: /medianexus
paths:
  config_dir: /config
  data_dir: /config/data
  db_dir: /config/data/db
  cache_dir: /config/data/cache
  backup_dir: /config/data/backups
security:
  auth_mode: proxy
  trusted_proxy_header: X-Remote-User
  trusted_proxy_cidrs: [10.0.0.0/8]
database:
  busy_timeout: 20s
  page_size: 8192
  cache_size_kb: 32768
  write_warn: 40ms
  write_hard: 3s
backup:
  schedule: "0 3 * * *"
  settings_on_change: true
  keep: 7
  keep_settings: 20
  before_migration: true
retention:
  decisions_full: 90d
  candidates: 168h
storage:
  min_free_bytes: 50GB
  min_free_percent: 5
workers:
  search: 8
  acquire: 3
  metadata: 6
  import: 1
logging:
  level: debug
  format: text
mode: observe
`
	parsed, err := config.Parse([]byte(document))
	if err != nil {
		t.Fatalf("Parse = %v, want nil", err)
	}

	if got, want := parsed.Database.BusyTimeout.AsDuration(), 20*time.Second; got != want {
		t.Errorf("BusyTimeout = %v, want %v", got, want)
	}
	if got, want := parsed.Retention.DecisionsFull.AsDuration(), 90*24*time.Hour; got != want {
		t.Errorf("DecisionsFull = %v, want %v — 'd' is not a Go duration unit and must be handled", got, want)
	}
	if got, want := parsed.Storage.MinFreeBytes.AsInt64(), int64(50_000_000_000); got != want {
		t.Errorf("MinFreeBytes = %d, want %d", got, want)
	}
	if parsed.Mode != config.ModeObserve {
		t.Errorf("Mode = %q, want observe", parsed.Mode)
	}
}

func problemsFrom(t *testing.T, document string) config.Problems {
	t.Helper()
	_, err := config.Parse([]byte(document))
	if err == nil {
		t.Fatal("Parse returned no error, want problems")
	}
	var problems config.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("Parse returned %T, want config.Problems", err)
	}
	return problems
}

func findProblem(problems config.Problems, path string) (config.Problem, bool) {
	for _, problem := range problems {
		if problem.Path == path {
			return problem, true
		}
	}
	return config.Problem{}, false
}

// §14.1's worked example: three unrelated faults, all reported together, so the
// user fixes the file once rather than restarting three times (Invariant 60).
func TestParse_ReportsEveryProblemAtOnce(t *testing.T) {
	problems := problemsFrom(t, `
version: 1
server:
  port: 70000
  prot: 8080
security:
  auth_mode: proxy
`)

	for _, path := range []string{"server.port", "server.prot", "security.auth_mode"} {
		if _, found := findProblem(problems, path); !found {
			t.Errorf("no problem reported for %s; got %v", path, problems)
		}
	}
	if len(problems) < 3 {
		t.Errorf("reported %d problems, want at least 3", len(problems))
	}
}

func TestParse_UnknownKeySuggestsTheNearestValidOne(t *testing.T) {
	problems := problemsFrom(t, "version: 1\nserver:\n  prot: 8080\n")

	problem, found := findProblem(problems, "server.prot")
	if !found {
		t.Fatalf("no problem for the unknown key; got %v", problems)
	}
	if problem.Expectation != "unknown key" {
		t.Errorf("Expectation = %q, want %q", problem.Expectation, "unknown key")
	}
	if problem.Suggestion != "port" {
		t.Errorf("Suggestion = %q, want %q", problem.Suggestion, "port")
	}
}

func TestParse_UnrelatedUnknownKeyGetsNoSuggestion(t *testing.T) {
	// A wrong guess is worse than none.
	problems := problemsFrom(t, "version: 1\nserver:\n  quixotic: 1\n")

	problem, found := findProblem(problems, "server.quixotic")
	if !found {
		t.Fatalf("no problem for the unknown key; got %v", problems)
	}
	if problem.Suggestion != "" {
		t.Errorf("Suggestion = %q, want none for an unrelated key", problem.Suggestion)
	}
}

func TestParse_UnknownTopLevelSectionIsReported(t *testing.T) {
	problems := problemsFrom(t, "version: 1\nindexers:\n  - name: nzbgeek\n")

	problem, found := findProblem(problems, "indexers")
	if !found {
		t.Fatalf("no problem for the unknown section; got %v", problems)
	}
	// Everything tunable lives in settings.db, so this is a common mistake.
	if problem.Expectation != "unknown key" {
		t.Errorf("Expectation = %q, want %q", problem.Expectation, "unknown key")
	}
}

func TestParse_NeverPrintsASecret(t *testing.T) {
	// Invariant 61: secrets never appear in validation errors or logs.
	const key = "base64:S3CR3T-VALUE-THAT-MUST-NOT-LEAK"
	problems := problemsFrom(t, "version: 1\nserver:\n  port: 70000\nsecurity:\n  encryption_key: "+key+"\n  encryption_keyy: "+key+"\n")

	rendered := problems.Error()
	if strings.Contains(rendered, "S3CR3T") {
		t.Fatalf("the encryption key appeared in the error report:\n%s", rendered)
	}
	problem, found := findProblem(problems, "security.encryption_keyy")
	if !found {
		t.Fatalf("the misspelled secret key was not reported; got %v", problems)
	}
	if strings.Contains(problem.Value, "S3CR3T") {
		t.Errorf("Value = %q, want it redacted", problem.Value)
	}
}

func TestParse_ValueRules(t *testing.T) {
	cases := []struct {
		name     string
		document string
		wantPath string
	}{
		{"port out of range", "version: 1\nserver:\n  port: 70000\n", "server.port"},
		{"port zero", "version: 1\nserver:\n  port: 0\n", "server.port"},
		{"bind not an address", "version: 1\nserver:\n  bind: not-an-ip\n", "server.bind"},
		{"base_url without a slash", "version: 1\nserver:\n  base_url: medianexus\n", "server.base_url"},
		{"relative path", "version: 1\npaths:\n  db_dir: data/db\n", "paths.db_dir"},
		{"unknown auth mode", "version: 1\nsecurity:\n  auth_mode: kerberos\n", "security.auth_mode"},
		{"bad CIDR", "version: 1\nsecurity:\n  auth_mode: local\n  trusted_proxy_cidrs: [10.0.0.0/64]\n", "security.trusted_proxy_cidrs[0]"},
		// The driver is a build tag's choice, so naming one in config.yml is a key
		// that does not exist rather than a value that is wrong (ADR-5).
		{"driver named in config", "version: 1\ndatabase:\n  driver: modernc\n", "database.driver"},
		{"page size not a power of two", "version: 1\ndatabase:\n  page_size: 5000\n", "database.page_size"},
		{"hard budget below warn", "version: 1\ndatabase:\n  write_warn: 5s\n  write_hard: 1s\n", "database.write_hard"},
		{"unparseable cron", "version: 1\nbackup:\n  schedule: \"every tuesday\"\n", "backup.schedule"},
		{"too few backups kept", "version: 1\nbackup:\n  keep: 2\n", "backup.keep"},
		{"pre-migration backup disabled", "version: 1\nbackup:\n  before_migration: false\n", "backup.before_migration"},
		{"candidates outlive decisions", "version: 1\nretention:\n  decisions_full: 24h\n  candidates: 48h\n", "retention.candidates"},
		{"free percent over 100", "version: 1\nstorage:\n  min_free_percent: 150\n", "storage.min_free_percent"},
		{"zero workers", "version: 1\nworkers:\n  search: 0\n", "workers.search"},
		{"import concurrency changed", "version: 1\nworkers:\n  import: 4\n", "workers.import"},
		{"unknown log level", "version: 1\nlogging:\n  level: verbose\n", "logging.level"},
		{"unknown log format", "version: 1\nlogging:\n  format: xml\n", "logging.format"},
		{"unknown mode", "version: 1\nmode: pretend\n", "mode"},
		{"version from the future", "version: 99\n", "version"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			problems := problemsFrom(t, testCase.document)
			if _, found := findProblem(problems, testCase.wantPath); !found {
				t.Errorf("no problem for %s; got %v", testCase.wantPath, problems)
			}
		})
	}
}

func TestParse_ProxyAuthWithoutTrustedCIDRsIsRejected(t *testing.T) {
	// Trusting a header without knowing who may set it lets anyone claim to be
	// any user. This is §14.1's cross-field example.
	problems := problemsFrom(t, "version: 1\nsecurity:\n  auth_mode: proxy\n")

	problem, found := findProblem(problems, "security.auth_mode")
	if !found {
		t.Fatalf("no problem reported; got %v", problems)
	}
	if !strings.Contains(problem.Expectation, "trusted_proxy_cidrs") {
		t.Errorf("Expectation = %q, want it to name trusted_proxy_cidrs", problem.Expectation)
	}
}

func TestParse_MalformedYAMLIsReportedAsSuch(t *testing.T) {
	problems := problemsFrom(t, "version: 1\nserver:\n\tport: 8080\n")
	if len(problems) != 1 {
		t.Fatalf("got %d problems for malformed YAML, want 1: %v", len(problems), problems)
	}
}

func TestProblems_ErrorRendersTheReport(t *testing.T) {
	problems := problemsFrom(t, "version: 1\nserver:\n  port: 70000\n  prot: 8080\n")
	rendered := problems.Error()

	for _, want := range []string{"config.yml:", "server.port", "70000", "prot", "did you mean", "Nothing has been started."} {
		if !strings.Contains(rendered, want) {
			t.Errorf("report does not contain %q:\n%s", want, rendered)
		}
	}
}

func TestParse_DurationAcceptsDays(t *testing.T) {
	parsed, err := config.Parse([]byte("version: 1\nretention:\n  decisions_full: 90d\n  candidates: 7d\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := parsed.Retention.Candidates.AsDuration(), 7*24*time.Hour; got != want {
		t.Errorf("Candidates = %v, want %v", got, want)
	}
}

func TestParse_ByteSizesUseDecimalUnits(t *testing.T) {
	// A disk sold as 4TB holds 4x10^12 bytes; a threshold that silently means
	// something else is a support problem.
	parsed, err := config.Parse([]byte("version: 1\nstorage:\n  min_free_bytes: 1TB\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := parsed.Storage.MinFreeBytes.AsInt64(), int64(1_000_000_000_000); got != want {
		t.Errorf("MinFreeBytes = %d, want %d", got, want)
	}
}

func TestParse_AnUnversionedDocumentIsTheFirstSchema(t *testing.T) {
	// Defaulting to CurrentVersion would make an old file skip every migration
	// between the two the moment CurrentVersion moves on (§14.2).
	parsed, err := config.Parse([]byte("server:\n  port: 8080\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Version != 1 {
		t.Errorf("Version = %d for an unversioned document, want 1", parsed.Version)
	}
}

func TestParse_SuggestsAcrossCommonTypoShapes(t *testing.T) {
	cases := []struct {
		document   string
		path       string
		suggestion string
	}{
		{"version: 1\nserver:\n  prot: 8080\n", "server.prot", "port"},          // transposition
		{"version: 1\nserver:\n  bnd: 0.0.0.0\n", "server.bnd", "bind"},         // omission
		{"version: 1\nserver:\n  portt: 8080\n", "server.portt", "port"},        // duplication
		{"version: 1\nlogging:\n  levle: info\n", "logging.levle", "level"},     //nolint:misspell // the typo is the test
		{"version: 1\nworkers:\n  serach: 4\n", "workers.serach", "search"},     // transposition
		{"version: 1\npaths:\n  cache_dr: /c\n", "paths.cache_dr", "cache_dir"}, // omission
	}

	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			problems := problemsFrom(t, testCase.document)
			problem, found := findProblem(problems, testCase.path)
			if !found {
				t.Fatalf("no problem reported; got %v", problems)
			}
			if problem.Suggestion != testCase.suggestion {
				t.Errorf("Suggestion = %q, want %q", problem.Suggestion, testCase.suggestion)
			}
		})
	}
}

func TestParse_AMalformedSecretLineIsNotEchoed(t *testing.T) {
	// The parser's context snippet is exactly the line the user got wrong,
	// which for an encryption key is the line that must not be echoed back.
	_, err := config.Parse([]byte("version: 1\nsecurity:\n  encryption_key: [S3CR3T: bad\n"))
	if err == nil {
		t.Fatal("malformed YAML was accepted")
	}
	if strings.Contains(err.Error(), "S3CR3T") {
		t.Errorf("the encryption key appeared in the error:\n%s", err)
	}
}
