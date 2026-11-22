package readinglist

import (
	"reflect"
	"strings"
	"testing"
)

const lanternList = `<?xml version="1.0"?>
<ReadingList><Name>Lantern Saga</Name><Books>
<Book Series="The Lantern" Number="1" Volume="2019" Year="2019"/>
<Book Series="The Lantern" Number="9" Volume="2019" Year="2019"/>
<Book Series="Ember" Number="2" Volume="2015" Year="2015"/>
<Book Series="Ember" Number="3" Volume="" Year="2016"/>
<Book Series="Unheld" Number="1" Volume="" Year=""/>
<Book Series="" Number="4"/>
</Books></ReadingList>`

func TestAReadingListIsMatchedToHeldSeriesAndIssues(t *testing.T) {
	list, err := ParseReadingList(strings.NewReader(lanternList))
	if err != nil || list.Name != "Lantern Saga" || len(list.Books) != 5 {
		t.Fatalf("parse = %+v, %v", list, err)
	}
	titles := []SeriesTitle{
		{WorkID: 1, Title: "The Lantern", Year: 2019, Canonical: true},
		{WorkID: 2, Title: "Ember", Year: 2015, Canonical: true},
		{WorkID: 3, Title: "Ember", Year: 2022, Canonical: true},
	}
	parts := map[string]int64{"1/issue:1": 11, "2/issue:2": 22, "3/issue:2": 32, "2/issue:3": 23, "3/issue:3": 33}
	got := MatchReadingList(list, titles, func(work int64, ref string) (int64, bool) {
		id, found := parts[string(rune('0'+work))+"/"+ref]
		return id, found
	})
	type short struct {
		Number, Status, Missing string
		Part                    int64
	}
	var summary []short
	for _, match := range got {
		summary = append(summary, short{match.Book.Number, match.Status, match.Missing, match.PartID})
	}
	want := []short{{"1", MatchFound, "", 11}, {"9", MatchNotFound, "issue", 0}, {"2", MatchFound, "", 22}, {"3", MatchAmbiguous, "", 0}, {"1", MatchNotFound, "series", 0}}
	if !reflect.DeepEqual(summary, want) {
		t.Fatalf("matches = %+v, want %+v", summary, want)
	}
}

func TestAReadingListIsRefusedWhenEmptyOrTooLarge(t *testing.T) {
	if _, err := ParseReadingList(strings.NewReader("<ReadingList><Name>x</Name></ReadingList>")); err == nil {
		t.Error("a list without books must be refused")
	}
	big := "<ReadingList>" + strings.Repeat(" ", MaxReadingListBytes) + "</ReadingList>"
	if _, err := ParseReadingList(strings.NewReader(big)); err == nil {
		t.Error("a list over the cap must be refused")
	}
}
