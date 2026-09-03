package paginate_test

import (
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/paginate"
)

func TestCursor_RoundTrips(t *testing.T) {
	original := paginate.Cursor{Sort: 1772000000, ID: 4711}

	decoded, err := paginate.Decode(original.Encode())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != original {
		t.Errorf("Decode(Encode(%+v)) = %+v", original, decoded)
	}
}

// An empty cursor is what a client sends on its first request, so it is the
// start of the list rather than an error.
func TestDecode_EmptyIsTheStart(t *testing.T) {
	cursor, err := paginate.Decode("")
	if err != nil {
		t.Fatalf("Decode(\"\") = %v, want nil", err)
	}
	if !cursor.Zero() {
		t.Errorf("Decode(\"\") = %+v, want the zero cursor", cursor)
	}
}

func TestDecode_RejectsGarbage(t *testing.T) {
	for _, raw := range []string{"not base64!!", "bm90LWEtY3Vyc29y", "eDp5", "MTIz"} {
		if _, err := paginate.Decode(raw); !errors.Is(err, paginate.ErrBadCursor) {
			t.Errorf("Decode(%q) = %v, want ErrBadCursor", raw, err)
		}
	}
}

func TestTextCursor_RoundTrips(t *testing.T) {
	original := paginate.TextCursor{Sort: "amber rivers", ID: 4711}

	decoded, err := paginate.DecodeText(original.Encode())
	if err != nil {
		t.Fatalf("DecodeText: %v", err)
	}
	if decoded != original {
		t.Errorf("DecodeText(Encode(%+v)) = %+v", original, decoded)
	}
}

// The sort value is arbitrary text and may itself contain ':', the id's own
// separator -- encoding puts the (always-numeric) id first specifically so a
// colon inside the title survives instead of truncating it.
func TestTextCursor_SortValueMayContainTheSeparator(t *testing.T) {
	original := paginate.TextCursor{Sort: "artist: the album", ID: 9}

	decoded, err := paginate.DecodeText(original.Encode())
	if err != nil {
		t.Fatalf("DecodeText: %v", err)
	}
	if decoded.Sort != original.Sort {
		t.Errorf("Sort = %q, want %q", decoded.Sort, original.Sort)
	}
}

func TestDecodeText_EmptyIsTheStart(t *testing.T) {
	cursor, err := paginate.DecodeText("")
	if err != nil {
		t.Fatalf("DecodeText(\"\") = %v, want nil", err)
	}
	if !cursor.Zero() {
		t.Errorf("DecodeText(\"\") = %+v, want the zero cursor", cursor)
	}
}

func TestDecodeText_RejectsGarbage(t *testing.T) {
	for _, raw := range []string{"not base64!!", "bm90LWEtY3Vyc29y", "eDp5"} {
		if _, err := paginate.DecodeText(raw); !errors.Is(err, paginate.ErrBadCursor) {
			t.Errorf("DecodeText(%q) = %v, want ErrBadCursor", raw, err)
		}
	}
}

func TestClampLimit(t *testing.T) {
	cases := map[int]int{
		0:    paginate.DefaultLimit, // an unset query parameter
		-5:   paginate.DefaultLimit,
		1:    1,
		50:   50,
		1000: paginate.MaxLimit, // asking for everything is how the process falls over
	}
	for requested, want := range cases {
		if got := paginate.ClampLimit(requested); got != want {
			t.Errorf("ClampLimit(%d) = %d, want %d", requested, got, want)
		}
	}
}

type row struct{ sort, id int64 }

func cursorOf(r row) paginate.Cursor { return paginate.Cursor{Sort: r.sort, ID: r.id} }

func TestBuild_LastPageHasNoCursor(t *testing.T) {
	// Fewer rows than the limit means there is nothing after them, so a client
	// stops without one more request that returns nothing.
	page := paginate.Build([]row{{1, 1}, {2, 2}}, 5, cursorOf)

	if len(page.Items) != 2 {
		t.Errorf("Items = %v, want both rows", page.Items)
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor = %q on the last page, want empty", page.NextCursor)
	}
}

func TestBuild_FullPageDropsTheProbeRowAndPointsAtIt(t *testing.T) {
	// Three rows were fetched for a limit of two: the extra one proves there is
	// more, and is discarded rather than returned.
	page := paginate.Build([]row{{10, 1}, {9, 2}, {8, 3}}, 2, cursorOf)

	if len(page.Items) != 2 {
		t.Fatalf("Items = %v, want exactly the limit", page.Items)
	}
	if page.Items[1] != (row{9, 2}) {
		t.Errorf("last item = %+v, want the second row", page.Items[1])
	}

	next, err := paginate.Decode(page.NextCursor)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// The cursor names the last *returned* row, not the probe row, or the
	// probe row would be skipped on the next page.
	if next != (paginate.Cursor{Sort: 9, ID: 2}) {
		t.Errorf("NextCursor = %+v, want the last returned row", next)
	}
}

func TestBuild_ExactlyTheLimitIsTheLastPage(t *testing.T) {
	page := paginate.Build([]row{{1, 1}, {2, 2}}, 2, cursorOf)
	if page.NextCursor != "" {
		t.Error("a page holding exactly the limit reported more to come")
	}
}

func TestBuild_EmptyResult(t *testing.T) {
	page := paginate.Build([]row{}, 10, cursorOf)
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Errorf("empty result produced %+v", page)
	}
}
