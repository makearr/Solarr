// Package paginate implements cursor pagination.
//
// OFFSET is banned (Invariant 32): the database must walk and discard every
// skipped row, so page 5,000 of a 500k-row library costs 5,000 pages of work.
// A cursor carries the position of the last row instead, so every page costs
// the same as the first.
//
// It also fixes a correctness problem OFFSET has and nobody notices: rows
// inserted or deleted between requests shift the window, so an item can be
// skipped or repeated while paging. A cursor is anchored to a row, not a count.
package paginate

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Limits on how much one request may ask for. The maximum exists because a
// client asking for everything is how a 500k-row library takes down the process
// (Invariant 32).
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// ErrBadCursor reports a cursor this server did not produce.
var ErrBadCursor = errors.New("paginate: cursor is not valid")

// Cursor is a position in a sorted list: the sort column's value, plus the row
// id to break ties.
//
// The id is not optional. Sorting by a non-unique column alone leaves rows with
// equal values in an arbitrary order, so a page boundary landing inside such a
// group silently drops or repeats rows.
type Cursor struct {
	Sort int64
	ID   int64
}

// Zero reports the start of the list.
func (c Cursor) Zero() bool { return c.Sort == 0 && c.ID == 0 }

// Encode renders a cursor for a client.
//
// It is opaque but not signed. A tampered cursor can only produce a different
// slice of data the caller is already permitted to see -- authorisation is
// applied by the repository regardless of where paging starts (Invariant 36) --
// so signing would add a key to manage for no gain.
func (c Cursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.FormatInt(c.Sort, 10) + ":" + strconv.FormatInt(c.ID, 10)))
}

// Decode parses a cursor. An empty string is the start of the list, which is
// what a client sends on its first request.
func Decode(raw string) (Cursor, error) {
	if raw == "" {
		return Cursor{}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: not base64", ErrBadCursor)
	}
	sortValue, id, found := strings.Cut(string(decoded), ":")
	if !found {
		return Cursor{}, fmt.Errorf("%w: malformed", ErrBadCursor)
	}

	var cursor Cursor
	if cursor.Sort, err = strconv.ParseInt(sortValue, 10, 64); err != nil {
		return Cursor{}, fmt.Errorf("%w: sort key is not a number", ErrBadCursor)
	}
	if cursor.ID, err = strconv.ParseInt(id, 10, 64); err != nil {
		return Cursor{}, fmt.Errorf("%w: id is not a number", ErrBadCursor)
	}
	return cursor, nil
}

// ClampLimit keeps a requested page size within bounds. A zero or negative
// limit takes the default rather than being an error: it is what an unset query
// parameter looks like.
func ClampLimit(requested int) int {
	switch {
	case requested <= 0:
		return DefaultLimit
	case requested > MaxLimit:
		return MaxLimit
	default:
		return requested
	}
}

// Page is one page of results.
type Page[T any] struct {
	Items []T `json:"items"`
	// NextCursor is empty on the last page, which is how a client knows to stop
	// without making one more request that returns nothing.
	NextCursor string `json:"next_cursor,omitempty"`
}

// Build assembles a page from one extra row than was asked for.
//
// Fetching limit+1 is how the last page is recognised without a second COUNT
// query: if the extra row exists there is more, and it is discarded rather than
// returned.
func Build[T any](rows []T, limit int, cursorOf func(T) Cursor) Page[T] {
	if len(rows) <= limit {
		return Page[T]{Items: rows}
	}

	items := rows[:limit]
	return Page[T]{Items: items, NextCursor: cursorOf(items[len(items)-1]).Encode()}
}
