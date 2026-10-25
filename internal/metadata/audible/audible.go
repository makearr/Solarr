// Package audible reads audiobooks as Audiobookshelf does: Audible's own catalog to search (api.audible.com, keyless)
// and Audnexus (api.audnex.us), the community service over it, for a book or an author by ASIN: narrators, runtime,
// series and position, publisher, genres, covers. It is the audiobook libraries' lead provider.
package audible

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means no book or author goes by that ASIN.
var ErrNotFound = errors.New("audible: not found")

const (
	catalogURL       = "https://api.audible"
	audnexusURL      = "https://api.audnex.us"
	maxResponseBytes = 8 << 20
	// SearchPageSize is the most one search answers.
	SearchPageSize = 20
	// searchGroups are the parts of each product the catalog sends.
	searchGroups = "contributors,product_attrs,product_desc,media,series"
)

// asinPattern is the only ASIN shape that reaches a request.
var asinPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// regionDomains are Audible's stores by region, as Audiobookshelf lists them.
var regionDomains = map[string]string{
	"us": ".com", "ca": ".ca", "uk": ".co.uk", "au": ".com.au", "fr": ".fr", "de": ".de", "jp": ".co.jp", "it": ".it", "in": ".in", "es": ".es",
}

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks both services; region picks the Audible store ("us" when it answers nothing).
type Client struct {
	catalog, audnexus Getter
	region            func() string
	catalogBase       string
	audnexusBase      string
}

// New builds a client over each service's own client, as each is its own rate bucket.
func New(catalog, audnexus Getter, region func() string) Client {
	return Client{catalog: catalog, audnexus: audnexus, region: region, catalogBase: catalogURL, audnexusBase: audnexusURL}
}

// IsASIN says value is shaped as an ASIN.
func IsASIN(value string) bool { return asinPattern.MatchString(strings.ToUpper(value)) }

// Person is an author or a narrator; ASIN is empty for most narrators.
type Person struct {
	ASIN string `json:"asin"`
	Name string `json:"name"`
}

// SeriesRef is a series a book belongs to, with its place in it ("1", "2.5").
type SeriesRef struct {
	ASIN     string `json:"asin"`
	Name     string `json:"name"`
	Position string `json:"position"`
}

// Genre is one of Audible's genres or tags.
type Genre struct {
	ASIN string `json:"asin"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Book is one audiobook as Audnexus describes it; dates are RFC 3339.
type Book struct {
	ASIN             string     `json:"asin"`
	Title            string     `json:"title"`
	Subtitle         string     `json:"subtitle"`
	Authors          []Person   `json:"authors"`
	Narrators        []Person   `json:"narrators"`
	Publisher        string     `json:"publisherName"`
	Summary          string     `json:"summary"`
	Description      string     `json:"description"`
	ReleaseDate      string     `json:"releaseDate"`
	Image            string     `json:"image"`
	Genres           []Genre    `json:"genres"`
	SeriesPrimary    *SeriesRef `json:"seriesPrimary"`
	SeriesSecondary  *SeriesRef `json:"seriesSecondary"`
	Language         string     `json:"language"`
	RuntimeLengthMin int        `json:"runtimeLengthMin"`
	FormatType       string     `json:"formatType"`
	ISBN             string     `json:"isbn"`
	Rating           string     `json:"rating"`
	IsAdult          bool       `json:"isAdult"`
}

// Abridged says the reading is abridged.
func (b Book) Abridged() bool { return strings.EqualFold(b.FormatType, "abridged") }

// Year is the year the reading came out, 0 when unknown.
func (b Book) Year() int {
	if len(b.ReleaseDate) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(b.ReleaseDate[:4])
	return year
}

// Author is one author as Audnexus describes them.
type Author struct {
	ASIN        string `json:"asin"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Image       string `json:"image"`
}

// Product is one book a catalog search found, in the catalog's own words.
type Product struct {
	ASIN        string            `json:"asin"`
	Title       string            `json:"title"`
	Subtitle    string            `json:"subtitle"`
	Authors     []Person          `json:"authors"`
	Narrators   []Person          `json:"narrators"`
	Publisher   string            `json:"publisher_name"`
	ReleaseDate string            `json:"release_date"`
	RuntimeMin  int               `json:"runtime_length_min"`
	Language    string            `json:"language"`
	FormatType  string            `json:"format_type"`
	Summary     string            `json:"merchandising_summary"`
	Images      map[string]string `json:"-"`
	Series      []struct {
		ASIN     string `json:"asin"`
		Title    string `json:"title"`
		Sequence string `json:"sequence"`
	} `json:"series"`
}

// Year is the year the product came out, 0 when unknown.
func (p Product) Year() int {
	if len(p.ReleaseDate) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(p.ReleaseDate[:4])
	return year
}

// Cover is the product's largest picture.
func (p Product) Cover() string {
	best, size := "", 0
	for key, address := range p.Images {
		if width, err := strconv.Atoi(key); err == nil && width > size && address != "" {
			best, size = address, width
		}
	}
	return best
}

type productJSON struct {
	Product
	ProductImages map[string]string `json:"product_images"`
}

// Search finds audiobooks by title and, when given, author, in Audible's store; an empty answer is not an error.
func (c Client) Search(ctx context.Context, title, author string, limit int) ([]Product, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}
	if limit <= 0 || limit > SearchPageSize {
		limit = SearchPageSize
	}
	query := url.Values{"num_results": {strconv.Itoa(limit)}, "products_sort_by": {"Relevance"}, "response_groups": {searchGroups}}
	if IsASIN(title) {
		query.Set("keywords", strings.ToUpper(title))
	} else {
		query.Set("title", title)
		if author != "" {
			query.Set("author", author)
		}
	}
	var found struct {
		Products []productJSON `json:"products"`
	}
	address := c.catalogBase + c.domain() + "/1.0/catalog/products?" + query.Encode()
	if err := get(ctx, c.catalog, address, &found); err != nil {
		return nil, err
	}
	products := make([]Product, 0, len(found.Products))
	for _, product := range found.Products {
		product.Images = product.ProductImages
		products = append(products, product.Product)
	}
	return products, nil
}

// Book reads one audiobook by ASIN from Audnexus.
func (c Client) Book(ctx context.Context, asin string) (Book, error) {
	asin = strings.ToUpper(strings.TrimSpace(asin))
	if !asinPattern.MatchString(asin) {
		return Book{}, ErrNotFound
	}
	var book Book
	if err := get(ctx, c.audnexus, c.audnexusBase+"/books/"+asin+c.regionQuery(), &book); err != nil {
		return Book{}, err
	}
	if book.ASIN == "" {
		return Book{}, ErrNotFound
	}
	return book, nil
}

// Author reads one author by ASIN from Audnexus.
func (c Client) Author(ctx context.Context, asin string) (Author, error) {
	asin = strings.ToUpper(strings.TrimSpace(asin))
	if !asinPattern.MatchString(asin) {
		return Author{}, ErrNotFound
	}
	var author Author
	if err := get(ctx, c.audnexus, c.audnexusBase+"/authors/"+asin+c.regionQuery(), &author); err != nil {
		return Author{}, err
	}
	if author.ASIN == "" {
		return Author{}, ErrNotFound
	}
	return author, nil
}

func (c Client) regionCode() string {
	if c.region != nil {
		if region := strings.ToLower(c.region()); regionDomains[region] != "" {
			return region
		}
	}
	return "us"
}

func (c Client) domain() string { return regionDomains[c.regionCode()] }

func (c Client) regionQuery() string { return "?" + url.Values{"region": {c.regionCode()}}.Encode() }

func get(ctx context.Context, client Getter, address string, into any) error {
	err := client.GetJSON(ctx, address, maxResponseBytes, into)
	switch {
	case errors.Is(err, httpx.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("audible: %w", err)
	}
	return nil
}
