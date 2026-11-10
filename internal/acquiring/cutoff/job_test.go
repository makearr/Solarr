package cutoff

import (
	"reflect"
	"testing"
)

func TestTallyKeepsTheFurthestFilesOnly(t *testing.T) {
	var tally Tally
	for percent := WorstKept + 5; percent > 0; percent-- {
		tally.add(Outcome{Judged: 1, BelowTarget: 1, Worst: []WorstFile{{WorkID: int64(percent), Percent: percent}}})
	}
	tally.add(Outcome{Judged: 2, BelowCutoff: 1})
	if tally.Judged != int64(WorstKept+5)+2 || tally.BelowTarget != int64(WorstKept+5) || tally.BelowCutoff != 1 {
		t.Fatalf("counts = %+v", tally)
	}
	var got []int
	for _, file := range tally.Worst {
		got = append(got, file.Percent)
	}
	if want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}; !reflect.DeepEqual(got, want) {
		t.Fatalf("worst percents = %v, want %v", got, want)
	}
}
