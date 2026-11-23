// Package specprovider runs a metadata provider that is entirely a YAML definition: requests, extraction, one generic Record.
package specprovider

// Record is what a definition's provider knows of one work; a media type's adapter turns it into that type's own record.
type Record struct {
	IDs                             map[string]string
	Title, OriginalTitle, SortTitle string
	AltTitles                       []string
	Overview                        string
	Year                            int
	Date, EndDate                   string
	Status                          string
	Language, Country               string
	Genres, Tags                    []string
	Adult                           bool
	AgeRating                       string
	Rating                          Rating
	PageCount, RuntimeSeconds       int
	Links                           []string
	Images                          []Image
	People                          []Person
	Organisations                   []Organisation
	Parts                           []Part
	// Dropped counts values a closed vocabulary had no word for.
	Dropped int
}

// Rating is a score on the provider's own scale, normalised by the adapter.
type Rating struct {
	Value float64
	Votes int
}

// Image is one picture.
type Image struct {
	Kind, URL, Language string
	Width, Height       int
}

// Person is one credit.
type Person struct{ Name, Role, ID string }

// Organisation is a studio, network or publisher.
type Organisation struct{ Name, Role string }

// Part is a volume, issue, episode or chapter of the work.
type Part struct {
	Kind, Title, Date, Overview string
	Number                      float64
	IDs                         map[string]string
}
