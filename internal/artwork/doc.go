// Package artwork resizes and encodes cover art to the three fixed sizes section
// 21.6 serves from cache/images/ (thumb 160px, small 400px, full 1000px), as WebP.
// Pure image processing: no I/O, no database, no HTTP -- a caller fetches the
// source bytes and writes the result to disk.
package artwork
