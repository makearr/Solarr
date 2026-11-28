package artwork

import "sync/atomic"

// Options are what a person sets for stored pictures (Settings -> Database): the longest edge of the Full size, the
// quality of all three sizes and the image format.
type Options struct {
	FullEdge int
	Quality  int
	Format   Format
}

// DefaultOptions are what stored covers were made with before they could be set.
var DefaultOptions = Options{FullEdge: int(Full), Quality: encodeQuality, Format: FormatWebP}

var configured atomic.Pointer[Options]

// Configure sets the options new covers are made with; zero fields keep the defaults.
func Configure(options Options) {
	if options.FullEdge <= 0 {
		options.FullEdge = DefaultOptions.FullEdge
	}
	if options.Quality <= 0 {
		options.Quality = DefaultOptions.Quality
	}
	if _, known := ParseFormat(string(options.Format)); !known {
		options.Format = DefaultOptions.Format
	}
	configured.Store(&options)
}

// Current is the options in force.
func Current() Options {
	if options := configured.Load(); options != nil {
		return *options
	}
	return DefaultOptions
}

// edgeOf is the longest edge a size is made at: Thumb and Small are fixed, Full follows the options.
func edgeOf(size Size) int {
	if size == Full {
		return Current().FullEdge
	}
	return int(size)
}

// Reencode decodes a stored cover and writes it again within the longest edge at the quality now in force.
func Reencode(source []byte, longestEdge int) ([]byte, error) {
	img, err := Decode(source)
	if err != nil {
		return nil, err
	}
	return Encode(ResizeLongestEdge(img, longestEdge))
}
