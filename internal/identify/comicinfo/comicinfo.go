// Package comicinfo reads the metadata a comic archive or folder carries: ComicInfo.xml, MetronInfo.xml and Mylar's series.json.
package comicinfo

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Provider keys of the ids a file can carry; each is the key used in work_external_id and work_part_external_id.
const (
	IDComicVineIssue  = "comicvine:issue"
	IDComicVineSeries = "comicvine:series"
	IDMetronIssue     = "metron:issue"
	IDMetronSeries    = "metron:series"
)

// ID is one provider id found in a file's metadata.
type ID struct{ Source, Value string }

// Info is what ComicInfo.xml or MetronInfo.xml says about one issue.
type Info struct {
	Series, Number, Title string
	Volume, Count, Year   int
	Month, Day            int
	// StoreDate is MetronInfo's in-store date as printed (YYYY-MM-DD); empty for ComicInfo.
	StoreDate              string
	Publisher, Imprint     string
	Web, Notes, GTIN       string
	Language               string
	Manga                  string
	AgeRating              string
	PageCount              int
	StoryArc, StoryArcNo   string
	SeriesGroup, Summary   string
	Genre                  string
	Credits                map[string][]string
	IDs                    []ID
	RightToLeft, MangaFlag bool
}

type comicInfoXML struct {
	Series         string `xml:"Series"`
	Number         string `xml:"Number"`
	Title          string `xml:"Title"`
	Volume         string `xml:"Volume"`
	Count          string `xml:"Count"`
	Year           string `xml:"Year"`
	Month          string `xml:"Month"`
	Day            string `xml:"Day"`
	Publisher      string `xml:"Publisher"`
	Imprint        string `xml:"Imprint"`
	Web            string `xml:"Web"`
	Notes          string `xml:"Notes"`
	GTIN           string `xml:"GTIN"`
	LanguageISO    string `xml:"LanguageISO"`
	Manga          string `xml:"Manga"`
	AgeRating      string `xml:"AgeRating"`
	PageCount      string `xml:"PageCount"`
	StoryArc       string `xml:"StoryArc"`
	StoryArcNumber string `xml:"StoryArcNumber"`
	SeriesGroup    string `xml:"SeriesGroup"`
	Summary        string `xml:"Summary"`
	Genre          string `xml:"Genre"`
	Writer         string `xml:"Writer"`
	Penciller      string `xml:"Penciller"`
	Inker          string `xml:"Inker"`
	Colorist       string `xml:"Colorist"`
	Letterer       string `xml:"Letterer"`
	CoverArtist    string `xml:"CoverArtist"`
	Editor         string `xml:"Editor"`
}

// maxXMLBytes bounds what one metadata file may hold.
const maxXMLBytes = 1 << 20

func newDecoder(data []byte) (*xml.Decoder, error) {
	if len(data) > maxXMLBytes {
		return nil, fmt.Errorf("comicinfo: metadata file larger than %d bytes", maxXMLBytes)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	return d, nil
}

// ParseComicInfo reads a ComicInfo.xml document.
func ParseComicInfo(data []byte) (Info, error) {
	d, err := newDecoder(data)
	if err != nil {
		return Info{}, err
	}
	var x comicInfoXML
	if err := d.Decode(&x); err != nil {
		return Info{}, fmt.Errorf("comicinfo: ComicInfo.xml: %w", err)
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }
	info := Info{
		Series: strings.TrimSpace(x.Series), Number: strings.TrimSpace(x.Number), Title: strings.TrimSpace(x.Title),
		Volume: atoi(x.Volume), Count: atoi(x.Count), Year: atoi(x.Year), Month: atoi(x.Month), Day: atoi(x.Day),
		Publisher: strings.TrimSpace(x.Publisher), Imprint: strings.TrimSpace(x.Imprint), Web: strings.TrimSpace(x.Web),
		Notes: strings.TrimSpace(x.Notes), GTIN: strings.TrimSpace(x.GTIN), Language: strings.ToLower(strings.TrimSpace(x.LanguageISO)),
		Manga: strings.TrimSpace(x.Manga), AgeRating: strings.TrimSpace(x.AgeRating), PageCount: atoi(x.PageCount),
		StoryArc: strings.TrimSpace(x.StoryArc), StoryArcNo: strings.TrimSpace(x.StoryArcNumber), SeriesGroup: strings.TrimSpace(x.SeriesGroup),
		Summary: strings.TrimSpace(x.Summary), Genre: strings.TrimSpace(x.Genre),
	}
	info.MangaFlag = strings.HasPrefix(strings.ToLower(info.Manga), "yes")
	info.RightToLeft = strings.EqualFold(info.Manga, "YesAndRightToLeft")
	info.Credits = map[string][]string{}
	for role, names := range map[string]string{"writer": x.Writer, "penciller": x.Penciller, "inker": x.Inker, "colorist": x.Colorist, "letterer": x.Letterer, "cover_artist": x.CoverArtist, "editor": x.Editor} {
		if list := splitNames(names); len(list) > 0 {
			info.Credits[role] = list
		}
	}
	info.IDs = idsFromText(info.Web, info.Notes)
	return info, nil
}

func splitNames(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

var (
	comicVineURL  = regexp.MustCompile(`comicvine\.gamespot\.com/[^\s"]*?(4000|4050)-(\d+)`)
	issueIDNote   = regexp.MustCompile(`(?i)\[Issue ID (\d+)\]`)
	cvdbNote      = regexp.MustCompile(`(?i)\[CVDB(\d+)\]`)
	metronIDNote  = regexp.MustCompile(`(?i)Metron (?:Issue |Series )?ID:? ?(\d+)`)
	metronURLPath = regexp.MustCompile(`metron\.cloud/(issue|series)/(\d+)`)
)

// idsFromText finds provider ids in a Web address and in Notes text.
func idsFromText(web, notes string) []ID {
	var ids []ID
	add := func(src, val string) {
		for _, have := range ids {
			if have.Source == src && have.Value == val {
				return
			}
		}
		ids = append(ids, ID{Source: src, Value: val})
	}
	if m := comicVineURL.FindStringSubmatch(web); m != nil {
		add(map[string]string{"4000": IDComicVineIssue, "4050": IDComicVineSeries}[m[1]], m[2])
	}
	if m := metronURLPath.FindStringSubmatch(web); m != nil {
		add(map[string]string{"issue": IDMetronIssue, "series": IDMetronSeries}[m[1]], m[2])
	}
	if m := issueIDNote.FindStringSubmatch(notes); m != nil {
		add(IDComicVineIssue, m[1])
	}
	if m := cvdbNote.FindStringSubmatch(notes); m != nil {
		add(IDComicVineIssue, m[1])
	}
	if m := metronIDNote.FindStringSubmatch(notes); m != nil {
		add(IDMetronIssue, m[1])
	}
	return ids
}

type metronIDXML struct {
	Source  string `xml:"source,attr"`
	Primary string `xml:"primary,attr"`
	Value   string `xml:",chardata"`
}

type metronInfoXML struct {
	IDs       []metronIDXML `xml:"IDS>ID"`
	Publisher struct {
		Name string `xml:"Name"`
	} `xml:"Publisher"`
	Series struct {
		Name   string `xml:"Name"`
		Volume string `xml:"Volume"`
		ID     string `xml:"id,attr"`
	} `xml:"Series"`
	Number     string `xml:"Number"`
	CoverDate  string `xml:"CoverDate"`
	StoreDate  string `xml:"StoreDate"`
	AgeRating  string `xml:"AgeRating"`
	PageCount  string `xml:"PageCount"`
	Summary    string `xml:"Summary"`
	Collection string `xml:"CollectionTitle"`
}

// ParseMetronInfo reads a MetronInfo.xml document into the same shape as ComicInfo.
func ParseMetronInfo(data []byte) (Info, error) {
	d, err := newDecoder(data)
	if err != nil {
		return Info{}, err
	}
	var x metronInfoXML
	if err := d.Decode(&x); err != nil {
		return Info{}, fmt.Errorf("comicinfo: MetronInfo.xml: %w", err)
	}
	info := Info{
		Series: strings.TrimSpace(x.Series.Name), Number: strings.TrimSpace(x.Number), Publisher: strings.TrimSpace(x.Publisher.Name),
		AgeRating: strings.TrimSpace(x.AgeRating), Summary: strings.TrimSpace(x.Summary), Title: strings.TrimSpace(x.Collection),
		StoreDate: strings.TrimSpace(x.StoreDate),
	}
	info.Volume, _ = strconv.Atoi(strings.TrimSpace(x.Series.Volume))
	info.PageCount, _ = strconv.Atoi(strings.TrimSpace(x.PageCount))
	info.Year, info.Month, info.Day = splitDate(x.CoverDate)
	for _, id := range x.IDs {
		value := strings.TrimSpace(id.Value)
		if value == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(id.Source)) {
		case "metron":
			info.IDs = append(info.IDs, ID{Source: IDMetronIssue, Value: value})
		case "comic vine", "comicvine":
			info.IDs = append(info.IDs, ID{Source: IDComicVineIssue, Value: value})
		}
	}
	if x.Series.ID != "" {
		info.IDs = append(info.IDs, ID{Source: IDMetronSeries, Value: strings.TrimSpace(x.Series.ID)})
	}
	return info, nil
}

func splitDate(s string) (year, month, day int) {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) > 0 {
		year, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		month, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		day, _ = strconv.Atoi(parts[2])
	}
	return year, month, day
}

// Series is what Mylar's series.json says about a series folder.
type Series struct {
	Name, Publisher, Type, Description string
	Year                               int
	ComicVineID                        string
}

type seriesJSON struct {
	Metadata struct {
		ComicID     looseNumber `json:"comicid"`
		Name        string      `json:"name"`
		Year        looseNumber `json:"year"`
		Publisher   string      `json:"publisher"`
		Type        string      `json:"booktype"`
		Description string      `json:"description_text"`
	} `json:"metadata"`
}

// looseNumber holds a JSON number or numeric string as text.
type looseNumber string

func (n *looseNumber) UnmarshalJSON(b []byte) error {
	*n = looseNumber(strings.Trim(strings.TrimSpace(string(b)), `"`))
	if *n == "null" {
		*n = ""
	}
	return nil
}

// ParseSeriesJSON reads Mylar's series.json; a number the file holds as text is accepted too.
func ParseSeriesJSON(data []byte) (Series, error) {
	if len(data) > maxXMLBytes {
		return Series{}, fmt.Errorf("comicinfo: series.json larger than %d bytes", maxXMLBytes)
	}
	var x seriesJSON
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &x); err != nil {
		return Series{}, fmt.Errorf("comicinfo: series.json: %w", err)
	}
	year, _ := strconv.Atoi(string(x.Metadata.Year))
	return Series{
		Name: strings.TrimSpace(x.Metadata.Name), Publisher: strings.TrimSpace(x.Metadata.Publisher), Type: strings.TrimSpace(x.Metadata.Type),
		Description: strings.TrimSpace(x.Metadata.Description), Year: year, ComicVineID: string(x.Metadata.ComicID),
	}, nil
}
