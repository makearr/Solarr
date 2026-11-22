// Package archive lists and extracts zip, tar (+gz/bz2/xz), rar and 7z in pure Go (ADR-49), keeping it in the static binary (ADR-1) and clear of UnRAR's licence; List never writes, Extract writes only into a caller-supplied workspace, and an archive inside a library root is listed as PACKED, never expanded (the caller's job).
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

// DetectFormat maps an archive's name to its format by extension, which a scene release, comic or backup carries more reliably than a magic-byte sniff is worth.
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

// Limits bounds Extract and has no permissive default (ADR-49): every field must be set, so a zero value cannot be passed by mistake and extract without bounds.
type Limits struct {
	// MaxExpansionRatio caps uncompressed-size / compressed-size, the
	// classic decompression-bomb guard.
	MaxExpansionRatio float64
	// MaxTotalBytes caps the sum of every extracted entry's uncompressed
	// size in one Extract call.
	MaxTotalBytes int64
	// MaxEntryCount caps how many entries one archive may contain.
	MaxEntryCount int
	// MaxDepth caps nested-archive expansion; Extract never recurses, so this gives a recursing caller (import inspection, unbuilt) one place to read the limit, and a call depth over it is refused.
	MaxDepth int
	// MinFreeBytes is the free-space margin required on the destination
	// filesystem, checked before the first byte is written.
	MinFreeBytes int64
}

// DefaultLimits is a usable starting point: a 200x expansion ratio catches a real zip bomb (often over 1000x) without refusing a lossless CD image re-archived.
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

// maxMultipartVolumes keeps malformed archive filenames from making a missing
// volume report iterate or allocate an unbounded number of entries.
const maxMultipartVolumes = 10_000

// MultipartSet is one detected multi-volume archive: the primary volume List
// and Extract should be pointed at, and every volume number this base name's
// siblings actually have (1-based, contiguous or not).
type MultipartSet struct {
	Base          string
	Primary       string
	Present       map[int]bool
	primaryVolume int
}

// Has reports whether name is a volume of the set (its primary included).
func (m MultipartSet) Has(name string) bool {
	if name == m.Primary {
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), strings.ToLower(m.Base)+".")
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
		if n == highest {
			break
		}
	}
	return missing
}

// DetectMultipart groups sibling names into multipart sets keyed by base name; a name matching none of the three schemes is its own single-volume archive.
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
		if set.primaryVolume == 0 || volume < set.primaryVolume || volume == set.primaryVolume && name < set.Primary {
			// A set missing its own first volume still needs a primary to
			// report MissingVolumes against; the lowest present volume
			// stands in until MissingVolumes names what is actually gone.
			set.primaryVolume = volume
			set.Primary = name
		}
	}

	for _, name := range names {
		switch {
		case partNumberedRe.MatchString(name):
			m := partNumberedRe.FindStringSubmatch(name)
			volume, err := strconv.Atoi(m[2])
			if err != nil || volume < 1 || volume > maxMultipartVolumes {
				continue
			}
			setPrimary(ensure(m[1]), volume, name)
		case sevenZipPartRe.MatchString(name):
			m := sevenZipPartRe.FindStringSubmatch(name)
			volume, err := strconv.Atoi(m[2])
			if err != nil || volume < 1 || volume > maxMultipartVolumes {
				continue
			}
			setPrimary(ensure(m[1]), volume, name)
		case rNumberedRe.MatchString(name):
			m := rNumberedRe.FindStringSubmatch(name)
			volume, err := strconv.Atoi(m[2])
			if err != nil || volume > maxMultipartVolumes-2 {
				continue
			}
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
