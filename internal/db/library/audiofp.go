package library

import (
	"encoding/binary"
	"math/bits"
)

// EncodeFingerprint stores Chromaprint values as little-endian 32-bit words; no values is an empty, non-nil value.
func EncodeFingerprint(values []uint32) []byte {
	encoded := make([]byte, 4*len(values))
	for index, value := range values {
		binary.LittleEndian.PutUint32(encoded[4*index:], value)
	}
	return encoded
}

// DecodeFingerprint is EncodeFingerprint's inverse; a trailing partial word is dropped.
func DecodeFingerprint(encoded []byte) []uint32 {
	values := make([]uint32, len(encoded)/4)
	for index := range values {
		values[index] = binary.LittleEndian.Uint32(encoded[4*index:])
	}
	return values
}

func prefixOf(values []uint32) uint32 {
	if len(values) == 0 {
		return 0
	}
	return values[0]
}

// fingerprintShift is how many Chromaprint values (about 0.12 s each) two encodes of one recording may be offset by.
const fingerprintShift = 3

// minFingerprintOverlap is the fewest values compared: shorter than that (about 12 s) proves nothing.
const minFingerprintOverlap = 100

// maxFingerprintErrorRate is the share of differing bits under which two fingerprints are one recording; unrelated
// audio sits near one half and re-encodes of one source well under a tenth.
const maxFingerprintErrorRate = 0.18

// SameRecording says whether two raw fingerprints are the same audio, allowing a small offset between them.
func SameRecording(a, b []uint32) bool {
	for shift := -fingerprintShift; shift <= fingerprintShift; shift++ {
		first, second := a, b
		if shift < 0 {
			first, second = b, a
		}
		offset := shift
		if offset < 0 {
			offset = -offset
		}
		if offset >= len(first) {
			continue
		}
		first = first[offset:]
		length := min(len(first), len(second))
		if length < minFingerprintOverlap {
			continue
		}
		differing := 0
		for index := 0; index < length; index++ {
			differing += bits.OnesCount32(first[index] ^ second[index])
		}
		if float64(differing)/float64(32*length) <= maxFingerprintErrorRate {
			return true
		}
	}
	return false
}
