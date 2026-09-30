package plan

import (
	"testing"
	"time"
)

func TestExplainGivesTheFirstReasonThatHolds(t *testing.T) {
	wanted := Circumstances{Monitored: true, State: "MISSING", Searchable: true, Indexers: 1, PerHour: 12, Planned: true}
	future := testNow.Add(48 * time.Hour).Unix()
	for name, test := range map[string]struct {
		change func(*Circumstances)
		want   Why
	}{
		"scheduled":          {func(*Circumstances) {}, WhyScheduled},
		"complete":           {func(c *Circumstances) { c.State = "COMPLETE" }, WhyNotWanted},
		"unmonitored":        {func(c *Circumstances) { c.Monitored = false }, WhyNotWanted},
		"downloading":        {func(c *Circumstances) { c.InFlight, c.PerHour = true, 0 }, WhyDownloading},
		"held":               {func(c *Circumstances) { c.Held = true }, WhyHeld},
		"no indexer":         {func(c *Circumstances) { c.Indexers, c.PerHour = 0, 0 }, WhyNoIndexer},
		"off":                {func(c *Circumstances) { c.PerHour = 0 }, WhyOff},
		"no room":            {func(c *Circumstances) { c.NoRoom = true }, WhyNoRoom},
		"not yet planned":    {func(c *Circumstances) { c.Planned = false }, WhyNotPlanned},
		"not yet released":   {func(c *Circumstances) { c.ReleaseDate = future }, WhyWaitingForRelease},
		"released long ago":  {func(c *Circumstances) { c.ReleaseDate = testNow.Add(-72 * time.Hour).Unix() }, WhyScheduled},
		"released this hour": {func(c *Circumstances) { c.ReleaseDate = testNow.Add(-time.Hour).Unix() }, WhyWaitingForRelease},
	} {
		c := wanted
		test.change(&c)
		if got := Explain(c, testNow); got != test.want {
			t.Errorf("%s: Explain = %s, want %s", name, got, test.want)
		}
	}
}
