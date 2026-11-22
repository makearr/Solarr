// Package internetarchive reads the Internet Archive's books (archive.org) for what an edition lacks: description, subjects,
// language and a cover. It needs no key.
package internetarchive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means the Archive holds no book under the ISBN.
var ErrNotFound = errors.New("internetarchive: not found")

const (
	baseURL          = "https://archive.org"
	maxResponseBytes = 2 << 20
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks the Archive, which needs no key.
type Client struct {
	http Getter
	base string
}

// New builds a client.
func New(client Getter) Client { return Client{http: client, base: baseURL} }

// Item is one book of the Archive.
type Item struct {
	Identifier  string
	Title       string
	Description string
	Subjects    []string
	Language    string
	Publisher   string
	Date        string
	// Cover is the Archive's own thumbnail address of the item.
	Cover string
}

// stringsOrOne is a field the Archive writes as a string or as a list of strings.
type stringsOrOne []string

func (s *stringsOrOne) UnmarshalJSON(raw []byte) error {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		*s = stringsOrOne{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

// ByISBN finds the text with the ISBN and reads its metadata.
func (c Client) ByISBN(ctx context.Context, isbn string) (Item, error) {
	if isbn == "" {
		return Item{}, ErrNotFound
	}
	var found struct {
		Response struct {
			Docs []struct {
				Identifier string `json:"identifier"`
			} `json:"docs"`
		} `json:"response"`
	}
	query := url.Values{"q": {"isbn:" + isbn + " AND mediatype:texts"}, "fl[]": {"identifier"}, "rows": {"1"}, "output": {"json"}}
	if err := c.get(ctx, "/advancedsearch.php?"+query.Encode(), &found); err != nil {
		return Item{}, err
	}
	if len(found.Response.Docs) == 0 || found.Response.Docs[0].Identifier == "" {
		return Item{}, ErrNotFound
	}
	return c.Item(ctx, found.Response.Docs[0].Identifier)
}

// Item reads one item by its identifier.
func (c Client) Item(ctx context.Context, identifier string) (Item, error) {
	var read struct {
		Metadata struct {
			Title       stringsOrOne `json:"title"`
			Description stringsOrOne `json:"description"`
			Subject     stringsOrOne `json:"subject"`
			Language    stringsOrOne `json:"language"`
			Publisher   stringsOrOne `json:"publisher"`
			Date        stringsOrOne `json:"date"`
		} `json:"metadata"`
	}
	if err := c.get(ctx, "/metadata/"+url.PathEscape(identifier), &read); err != nil {
		return Item{}, err
	}
	meta := read.Metadata
	if len(meta.Title) == 0 {
		return Item{}, ErrNotFound
	}
	return Item{
		Identifier: identifier, Title: meta.Title[0], Description: longest(meta.Description), Subjects: meta.Subject,
		Language: first(meta.Language), Publisher: first(meta.Publisher), Date: first(meta.Date),
		Cover: c.base + "/services/img/" + url.PathEscape(identifier),
	}, nil
}

func (c Client) get(ctx context.Context, path string, into any) error {
	err := c.http.GetJSON(ctx, c.base+path, maxResponseBytes, into)
	switch {
	case errors.Is(err, httpx.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("internetarchive: %w", err)
	}
	return nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// longest is the entry that reads as a blurb: the Archive also lists the physical description ("90 pages : 20 cm") there.
func longest(values []string) string {
	best := ""
	for _, value := range values {
		if len(strings.TrimSpace(value)) > len(best) {
			best = strings.TrimSpace(value)
		}
	}
	if len(best) < 60 {
		return ""
	}
	return best
}
