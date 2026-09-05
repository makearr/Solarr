// Package archive lists and extracts zip, tar (+gz/bz2/xz), rar and 7z
// archives, in pure Go (ADR-49): no CGO, no bundled unrar or p7zip, which
// keeps the capability inside the static binary (ADR-1) and outside the
// UnRAR licence's own restrictions on reimplementing the format. List never
// writes; Extract only ever into a caller-supplied workspace directory,
// never a library root (ADR-49 part 2 -- an archive found inside a library
// root is listed and counted as PACKED presence, never expanded, which is a
// caller's job, not this package's).
package archive

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Format names what List/Extract detected an archive to be.
type Format string

// Every format this package can read.
const (
	FormatZip    Format = "zip"
	FormatTar    Format = "tar"
	FormatTarGz  Format = "tar.gz"
	FormatTarBz2 Format = "tar.bz2"
	FormatTarXz  Format = "tar.xz"
	FormatRar    Format = "rar"
	Format7z     Format = "7z"
)

// DetectFormat maps an archive's own name to the format that reads it, by
// extension. A scene release, a comic (.cbz/.cbr) and a backup all carry a
// real, meaningful extension far more reliably than a magic-byte sniff would
// be worth the extra code to bother with.
func DetectFormat(name string) (Format, bool) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return FormatTarGz, true
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"):
		return FormatTarBz2, true
	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		return FormatTarXz, true
	case strings.HasSuffix(lower, ".tar"):
		return FormatTar, true
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".cbz"):
		return FormatZip, true
	case strings.HasSuffix(lower, ".rar"), strings.HasSuffix(lower, ".cbr"):
		return FormatRar, true
	case strings.HasSuffix(lower, ".7z"):
		return Format7z, true
	default:
		return "", false
	}
}

// Entry is one file inside an archive, from List -- no writes, ever. CRC32 is
// zero and HasCRC32 false for a format that does not carry one per entry
// (rar's FileHeader has none).
type Entry struct {
	Name      string
	SizeBytes int64
	CRC32     uint32
	HasCRC32  bool
	IsDir     bool
	// Encrypted marks an entry this package can list but cannot extract
	// without a password ADR-49 says to refuse rather than guess at.
	Encrypted bool
}

// ErrEncrypted reports an entry Extract refuses because it is encrypted and
// no password was supplied.
var ErrEncrypted = fmt.Errorf("archive: entry is encrypted")

// Limits bounds Extract. ADR-49: "not optional and has no permissive
// default" -- every field must be set to something real by the caller,
// which is why there is no zero-value Limits a caller could pass by mistake
// and get an unbounded extraction.
type Limits struct {
	// MaxExpansionRatio caps uncompressed-size / compressed-size, the
	// classic decompression-bomb guard.
	MaxExpansionRatio float64
	// MaxTotalBytes caps the sum of every extracted entry's uncompressed
	// size in one Extract call.
	MaxTotalBytes int64
	// MaxEntryCount caps how many entries one archive may contain.
	MaxEntryCount int
	// MaxDepth caps nested-archive expansion (an archive inside an
	// archive). Extract itself never recurses -- this field exists so a
	// caller that does (import inspection, unbuilt) has one place to read
	// the limit from; Extract enforces it only in the sense that Extract
	// call depth over this value is refused.
	MaxDepth int
	// MinFreeBytes is the free-space margin required on the destination
	// filesystem, checked before the first byte is written.
	MinFreeBytes int64
}

// DefaultLimits is a real, usable starting point -- not a permissive
// do-nothing default: a 200x expansion ratio catches an actual zip bomb
// (which routinely exceeds 1000x) while never refusing a real FLAC-in-a-CD
// image or similar already-compressed content re-archived losslessly.
func DefaultLimits() Limits {
	return Limits{
		MaxExpansionRatio: 200,
		MaxTotalBytes:     20 << 30, // 20 GiB
		MaxEntryCount:     100_000,
		MaxDepth:          3,
		MinFreeBytes:      1 << 30, // 1 GiB
	}
}

// multipartRe recognises the three multipart naming schemes ADR-49 names:
// "Album.part01.rar" (and "part1", no leading zero), "Album.rar"/"Album.r00"
// and "Album.7z.001".
var (
	partNumberedRe = regexp.MustCompile(`(?i)^(.+)\.part(\d+)\.rar$`)
	rNumberedRe    = regexp.MustCompile(`(?i)^(.+)\.r(\d{2,})$`)
	sevenZipPartRe = regexp.MustCompile(`(?i)^(.+\.7z)\.(\d{3})$`)
)

// MultipartSet is one detected multi-volume archive: the primary volume List
// and Extract should be pointed at, and every volume number this base name's
// siblings actually have (1-based, contiguous or not).
type MultipartSet struct {
	Base    string
	Primary string
	Present map[int]bool
}

// MissingVolumes reports which volume numbers, from 1 up to the highest one
// actually present, are absent -- "part 3 of 8 missing", never just
// "corrupt" (ADR-49's own wording).
func (m MultipartSet) MissingVolumes() []int {
	highest := 0
	for n := range m.Present {
		if n > highest {
			highest = n
		}
	}
	var missing []int
	for n := 1; n <= highest; n++ {
		if !m.Present[n] {
			missing = append(missing, n)
		}
	}
	return missing
}

// DetectMultipart groups names (siblings in one directory, as returned by a
// directory listing) into multipart sets, keyed by base name. A name that
// matches none of the three schemes is not part of any set -- the caller
// treats it as its own single-volume archive.
func DetectMultipart(names []string) map[string]*MultipartSet {
	sets := make(map[string]*MultipartSet)

	ensure := func(base string) *MultipartSet {
		set, ok := sets[base]
		if !ok {
			set = &MultipartSet{Base: base, Present: map[int]bool{}}
			sets[base] = set
		}
		return set
	}
	setPrimary := func(set *MultipartSet, volume int, name string) {
		set.Present[volume] = true
		if volume == 1 {
			set.Primary = name
		} else if set.Primary == "" {
			// A set missing its own first volume still needs a primary to
			// report MissingVolumes against; the lowest present volume
			// stands in until MissingVolumes names what is actually gone.
			set.Primary = name
		}
	}

	for _, name := range names {
		switch {
		case partNumberedRe.MatchString(name):
			m := partNumberedRe.FindStringSubmatch(name)
			volume, _ := strconv.Atoi(m[2])
			setPrimary(ensure(m[1]), volume, name)
		case sevenZipPartRe.MatchString(name):
			m := sevenZipPartRe.FindStringSubmatch(name)
			volume, _ := strconv.Atoi(m[2])
			setPrimary(ensure(m[1]), volume, name)
		case rNumberedRe.MatchString(name):
			m := rNumberedRe.FindStringSubmatch(name)
			volume, _ := strconv.Atoi(m[2])
			// .r00 is volume 2 (the .rar itself, found separately below, is
			// volume 1) -- rNN counts up from 00 for the second volume on.
			setPrimary(ensure(m[1]), volume+2, name)
		case strings.HasSuffix(strings.ToLower(name), ".rar"):
			base := name[:len(name)-len(".rar")]
			// Only claim this .rar as part of an r00-style set if a sibling
			// .r00 exists to confirm it -- otherwise every standalone .rar
			// would spuriously "detect" as a one-volume set.
			if hasRNumberedSibling(names, base) {
				setPrimary(ensure(base), 1, name)
			}
		}
	}
	return sets
}

func hasRNumberedSibling(names []string, base string) bool {
	for _, name := range names {
		if rNumberedRe.MatchString(name) {
			m := rNumberedRe.FindStringSubmatch(name)
			if m[1] == base {
				return true
			}
		}
	}
	return false
}
