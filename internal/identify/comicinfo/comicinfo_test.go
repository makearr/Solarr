package comicinfo

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseComicInfoWhole(t *testing.T) {
	doc := "\xef\xbb\xbf<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<ComicInfo>\n" +
		"<Series> Night Harbor </Series><Number>1.5</Number><Volume>2</Volume><Count>6</Count><Year>2023</Year><Month>4</Month><Day>12</Day>" +
		"<Title>Dawn</Title><Publisher>Acme Press</Publisher><Imprint>Acme Noir</Imprint>" +
		"<Web>https://comicvine.gamespot.com/night-harbor-15/4000-777001/</Web>" +
		"<Notes>Tagged with a tool [Issue ID 777001] [CVDB777001]</Notes><LanguageISO>EN</LanguageISO><Manga>YesAndRightToLeft</Manga>" +
		"<AgeRating>Teen</AgeRating><PageCount>24</PageCount><StoryArc>Long Night</StoryArc><StoryArcNumber>3</StoryArcNumber>" +
		"<Writer>Ann Quill, Bo Reed</Writer><Penciller>Cy Dash</Penciller></ComicInfo>"
	got, err := ParseComicInfo([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := Info{
		Series: "Night Harbor", Number: "1.5", Title: "Dawn", Volume: 2, Count: 6, Year: 2023, Month: 4, Day: 12,
		Publisher: "Acme Press", Imprint: "Acme Noir", Web: "https://comicvine.gamespot.com/night-harbor-15/4000-777001/",
		Notes: "Tagged with a tool [Issue ID 777001] [CVDB777001]", Language: "en", Manga: "YesAndRightToLeft", AgeRating: "Teen",
		PageCount: 24, StoryArc: "Long Night", StoryArcNo: "3",
		Credits:     map[string][]string{"writer": {"Ann Quill", "Bo Reed"}, "penciller": {"Cy Dash"}},
		IDs:         []ID{{IDComicVineIssue, "777001"}},
		RightToLeft: true, MangaFlag: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseComicInfoSeriesAddress(t *testing.T) {
	got, err := ParseComicInfo([]byte(`<ComicInfo><Web>https://comicvine.gamespot.com/x/4050-1234/</Web></ComicInfo>`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []ID{{IDComicVineSeries, "1234"}}; !reflect.DeepEqual(got.IDs, want) {
		t.Fatalf("got %v want %v", got.IDs, want)
	}
}

func TestParseMalformed(t *testing.T) {
	for name, doc := range map[string]string{
		"empty":     "",
		"truncated": "<ComicInfo><Series>Night",
		"not xml":   "{}",
		"wrong tag": "<Other/>x<",
		"huge":      strings.Repeat("a", maxXMLBytes+1),
	} {
		if _, err := ParseComicInfo([]byte(doc)); err == nil && name != "wrong tag" {
			t.Errorf("%s: want an error", name)
		}
		if _, err := ParseMetronInfo([]byte(doc)); err == nil && name != "wrong tag" {
			t.Errorf("%s (metron): want an error", name)
		}
	}
	if _, err := ParseSeriesJSON([]byte(`{"metadata":`)); err == nil {
		t.Error("truncated series.json: want an error")
	}
}

func TestParseMetronInfoWhole(t *testing.T) {
	doc := `<MetronInfo><IDS><ID source="Metron" primary="true">9001</ID><ID source="Comic Vine">777001</ID></IDS>
<Publisher><Name>Acme Press</Name></Publisher><Series id="55"><Name>Night Harbor</Name><Volume>2</Volume></Series>
<Number>7</Number><CoverDate>2023-06-01</CoverDate><StoreDate>2023-04-12</StoreDate><AgeRating>Teen</AgeRating><PageCount>32</PageCount></MetronInfo>`
	got, err := ParseMetronInfo([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := Info{
		Series: "Night Harbor", Number: "7", Volume: 2, Year: 2023, Month: 6, Day: 1, StoreDate: "2023-04-12", Publisher: "Acme Press",
		AgeRating: "Teen", PageCount: 32,
		IDs: []ID{{IDMetronIssue, "9001"}, {IDComicVineIssue, "777001"}, {IDMetronSeries, "55"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseSeriesJSON(t *testing.T) {
	for _, doc := range []string{
		`{"metadata":{"type":"comicSeries","comicid":4321,"name":"Night Harbor","year":2019,"publisher":"Acme Press","booktype":"Print"}}`,
		`{"metadata":{"comicid":"4321","name":"Night Harbor","year":"2019","publisher":"Acme Press","booktype":"Print"}}`,
	} {
		got, err := ParseSeriesJSON([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		want := Series{Name: "Night Harbor", Publisher: "Acme Press", Type: "Print", Year: 2019, ComicVineID: "4321"}
		if got != want {
			t.Fatalf("got %+v want %+v", got, want)
		}
	}
}
