package library_test

import (
	"math/rand"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func noisy(values []uint32, flipEvery int, seed int64) []uint32 {
	random := rand.New(rand.NewSource(seed))
	out := slices.Clone(values)
	for index := range out {
		if index%flipEvery == 0 {
			out[index] ^= 1 << random.Intn(32)
		}
	}
	return out
}

func sample(length int, seed int64) []uint32 {
	random := rand.New(rand.NewSource(seed))
	values := make([]uint32, length)
	for index := range values {
		values[index] = random.Uint32()
	}
	return values
}

func TestEncodeFingerprint_RoundTrips(t *testing.T) {
	values := []uint32{1, 0xdeadbeef, 0}
	if got := library.DecodeFingerprint(library.EncodeFingerprint(values)); !slices.Equal(got, values) {
		t.Errorf("round trip = %v, want %v", got, values)
	}
	if encoded := library.EncodeFingerprint(nil); encoded == nil || len(encoded) != 0 {
		t.Errorf("no values = %#v, want an empty non-nil value", encoded)
	}
}

func TestSameRecording(t *testing.T) {
	original := sample(900, 1)
	for _, test := range []struct {
		name string
		a, b []uint32
		want bool
	}{
		{"identical", original, slices.Clone(original), true},
		{"a re-encode with a few flipped bits", original, noisy(original, 2, 9), true},
		{"offset by two values", original, original[2:], true},
		{"unrelated audio", original, sample(900, 2), false},
		{"too short to compare", original[:50], original[:50], false},
		{"nothing", nil, original, false},
	} {
		if got := library.SameRecording(test.a, test.b); got != test.want {
			t.Errorf("%s: SameRecording = %v, want %v", test.name, got, test.want)
		}
	}
}
