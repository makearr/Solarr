// Package logbuffer keeps the most recent log records in memory so the
// interface can show them without a log file, a shipper or a second sink
// (revision 7.16).
package logbuffer

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Record is one captured log line, flattened: the interface shows text, and
// slog.Attr values are not worth keeping alive once formatted.
type Record struct {
	Time    time.Time
	Level   string
	Message string
	// Attrs is the record's own attributes rendered as "key=value" pairs, in
	// the order slog emitted them.
	Attrs []string
}

// buffer is the records themselves, shared by every handler derived from one
// Ring. Separate from Ring because WithAttrs/WithGroup must return a handler
// that writes into the *same* buffer -- copying the slice header instead sends
// a derived logger's records somewhere nobody reads.
type buffer struct {
	capacity int

	mu      sync.RWMutex
	records []Record
	// start is the oldest record's index once the ring has wrapped.
	start int
	full  bool
}

// Ring is an slog.Handler that keeps the last capacity records and passes
// every one on to the handler it wraps, so adding it never costs a log line.
// Bounded by count rather than bytes: a record is small and a count is the
// thing an operator can reason about ("the last 2,000 lines").
type Ring struct {
	next slog.Handler
	buf  *buffer
	// attrs and group are what With and WithGroup added, which the record itself never carries.
	attrs []string
	group string
}

// NewRing wraps next, keeping at most capacity records.
func NewRing(next slog.Handler, capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{next: next, buf: &buffer{capacity: capacity, records: make([]Record, 0, capacity)}}
}

// Enabled defers entirely to the wrapped handler: the ring shows what was
// logged, never more, so raising the log level quiets it too.
func (r *Ring) Enabled(ctx context.Context, level slog.Level) bool {
	return r.next.Enabled(ctx, level)
}

// Handle records the line and passes it on. A failure to pass it on is still
// returned, but the record is kept either way -- the buffer's whole purpose is
// being readable when the real sink is not.
func (r *Ring) Handle(ctx context.Context, record slog.Record) error {
	captured := Record{Time: record.Time, Level: record.Level.String(), Message: record.Message}
	captured.Attrs = append(captured.Attrs, r.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		captured.Attrs = append(captured.Attrs, r.group+attr.Key+"="+attr.Value.String())
		return true
	})

	r.buf.mu.Lock()
	if len(r.buf.records) < r.buf.capacity {
		r.buf.records = append(r.buf.records, captured)
	} else {
		r.buf.records[r.buf.start] = captured
		r.buf.start = (r.buf.start + 1) % r.buf.capacity
		r.buf.full = true
	}
	r.buf.mu.Unlock()

	return r.next.Handle(ctx, record)
}

// WithAttrs and WithGroup wrap the next handler and share this ring, so a
// logger derived with .With() still lands in the same buffer.
func (r *Ring) WithAttrs(attrs []slog.Attr) slog.Handler {
	formatted := slices.Clone(r.attrs)
	for _, attr := range attrs {
		formatted = append(formatted, r.group+attr.Key+"="+attr.Value.String())
	}
	return &Ring{next: r.next.WithAttrs(attrs), buf: r.buf, attrs: formatted, group: r.group}
}

// WithGroup mirrors WithAttrs; the group prefixes later keys as the text handler does ("group.key").
func (r *Ring) WithGroup(name string) slog.Handler {
	if name == "" {
		return r
	}
	return &Ring{next: r.next.WithGroup(name), buf: r.buf, attrs: r.attrs, group: r.group + name + "."}
}

// Filter narrows what Records answers.
type Filter struct {
	// MinLevel drops anything below it. Zero means everything captured.
	MinLevel slog.Level
	// Contains matches the message or any attribute, case-insensitively.
	// Empty matches everything.
	Contains string
	// Limit caps how many are returned, newest first. Zero means all of them.
	Limit int
}

// Records answers the buffered records newest first, filtered.
func (r *Ring) Records(filter Filter) []Record {
	r.buf.mu.RLock()
	ordered := r.buf.orderedLocked()
	r.buf.mu.RUnlock()

	needle := strings.ToLower(filter.Contains)
	matched := make([]Record, 0, len(ordered))
	for i := len(ordered) - 1; i >= 0; i-- {
		record := ordered[i]
		if !levelAtLeast(record.Level, filter.MinLevel) {
			continue
		}
		if needle != "" && !matches(record, needle) {
			continue
		}
		matched = append(matched, record)
		if filter.Limit > 0 && len(matched) == filter.Limit {
			break
		}
	}
	return matched
}

// orderedLocked answers the records oldest first, unwrapping the ring.
func (b *buffer) orderedLocked() []Record {
	if !b.full {
		return append([]Record(nil), b.records...)
	}
	ordered := make([]Record, 0, len(b.records))
	ordered = append(ordered, b.records[b.start:]...)
	return append(ordered, b.records[:b.start]...)
}

func matches(record Record, lowercaseNeedle string) bool {
	if strings.Contains(strings.ToLower(record.Message), lowercaseNeedle) {
		return true
	}
	for _, attr := range record.Attrs {
		if strings.Contains(strings.ToLower(attr), lowercaseNeedle) {
			return true
		}
	}
	return false
}

// levelAtLeast compares the stored level name back against a threshold.
// Stored as a string because that is what the interface shows; the set is
// fixed and tiny, so parsing it back is cheaper than keeping both.
func levelAtLeast(name string, minimum slog.Level) bool {
	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		// An unrecognised level is shown rather than hidden: losing a line
		// because its label was unexpected is the worse failure.
		return true
	}
	return level >= minimum
}
