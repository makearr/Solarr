// Package paginate implements cursor pagination (Invariant 32).
package paginate

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Limits on how much one request may ask for (Invariant 32).
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// ErrBadCursor reports a cursor this server did not produce.
var ErrBadCursor = errors.New("paginate: cursor is not valid")

// Cursor is a position in a sorted list: the sort column's value, plus the row id to break
// ties.
type Cursor struct {
	Sort int64
	ID   int64
}

// Zero reports the start of the list.
func (c Cursor) Zero() bool { return c.Sort == 0 && c.ID == 0 }

// Encode renders a cursor for a client (Invariant 36).
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

// TextCursor is Cursor's counterpart for a list sorted by a text column (a
// work's sort_title, e.g.) rather than an integer one.
type TextCursor struct {
	Sort string
	ID   int64
}

// Zero reports the start of the list.
func (c TextCursor) Zero() bool { return c.Sort == "" && c.ID == 0 }

// Encode renders a cursor for a client. The id comes first and is always
// numeric, so decoding can split on the first ':' and leave every remaining
// character -- colons included -- as part of Sort.
func (c TextCursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.FormatInt(c.ID, 10) + ":" + c.Sort))
}

// DecodeText parses a cursor Encode produced. An empty string is the start of
// the list, matching Decode.
func DecodeText(raw string) (TextCursor, error) {
	if raw == "" {
		return TextCursor{}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return TextCursor{}, fmt.Errorf("%w: not base64", ErrBadCursor)
	}
	id, sortValue, found := strings.Cut(string(decoded), ":")
	if !found {
		return TextCursor{}, fmt.Errorf("%w: malformed", ErrBadCursor)
	}

	var cursor TextCursor
	if cursor.ID, err = strconv.ParseInt(id, 10, 64); err != nil {
		return TextCursor{}, fmt.Errorf("%w: id is not a number", ErrBadCursor)
	}
	cursor.Sort = sortValue
	return cursor, nil
}

// ClampLimit keeps a requested page size within bounds.
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
func Build[T any](rows []T, limit int, cursorOf func(T) Cursor) Page[T] {
	if len(rows) <= limit {
		return Page[T]{Items: rows}
	}

	items := rows[:limit]
	return Page[T]{Items: items, NextCursor: cursorOf(items[len(items)-1]).Encode()}
}
