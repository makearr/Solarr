package app

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/providerdef"
)

func indexerDescriptor(t *testing.T, source providerdef.Source, extra string) providerdef.Definition {
	t.Helper()
	data := []byte("schema: 1\nkind: indexer\nkey: sample-tracker\nname: Sample Tracker\nengine: builtin\n" + extra)
	def, problems := providerdef.Parse(providerdef.KindIndexer, "sample-tracker.yaml", data, source)
	for _, problem := range problems {
		if problem.Severity == providerdef.SeverityError {
			t.Fatalf("descriptor: %v", problems)
		}
	}
	return def
}

func TestACustomDescriptorsRequestDelayBecomesThePacingFloor(t *testing.T) {
	builtin := indexerDescriptor(t, providerdef.SourceBuiltin, "")
	cases := []struct {
		name, extra string
		want        time.Duration
	}{
		{"two seconds", "request_delay: 2s\n", 2 * time.Second},
		{"not a duration", "request_delay: soon\n", 0},
		{"negative", "request_delay: -3s\n", 0},
		{"unset", "", 0},
	}
	for _, one := range cases {
		catalog, _ := providerdef.BuildCatalog([]providerdef.Definition{builtin}, []providerdef.Definition{indexerDescriptor(t, providerdef.SourceCustom, one.extra)})
		if got := descriptorRequestDelay(catalog, "sample-tracker"); got != one.want {
			t.Errorf("%s: delay = %s, want %s", one.name, got, one.want)
		}
	}

	pacing := newIndexerPacing(clock.NewFakeAtEpoch())
	pacing.setLimit(1, 3600)
	if got, _ := pacing.interval(1); got != 1250*time.Millisecond {
		t.Fatalf("limit alone paced at %s, want 1.25s", got)
	}
	pacing.setFloor(1, 5*time.Second)
	if got, _ := pacing.interval(1); got != 5*time.Second {
		t.Errorf("with a 5s floor the gap is %s, want 5s", got)
	}
	pacing.setFloor(1, 0)
	if got, _ := pacing.interval(1); got != 1250*time.Millisecond {
		t.Errorf("a cleared floor leaves %s, want 1.25s", got)
	}
}
