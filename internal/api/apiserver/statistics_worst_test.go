package apiserver

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

func TestWorstFilesReadsKeysClosestLastAndKeepsPipesInTitles(t *testing.T) {
	ref := "S01E02"
	got := worstFiles([]StatisticsFigure{
		{Key: "tv|7|S01E02|62|A | B", Value: 62},
		{Key: "movie|9||14|Heat", Value: 14},
		{Key: "broken", Value: 1},
	})
	want := []gen.StatisticsWorstFile{
		{MediaType: "movie", WorkId: 9, Title: "Heat", Percent: 14},
		{MediaType: "tv", WorkId: 7, MediaRef: &ref, Title: "A | B", Percent: 62},
	}
	if got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("worstFiles = %+v, want %+v", got, want)
	}
}
