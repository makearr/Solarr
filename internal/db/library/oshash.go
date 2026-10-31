package library

import (
	"encoding/binary"
	"fmt"

	"github.com/makearr/Solarr/internal/platform/filekind"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// oshashBlockBytes is the head and the tail the OpenSubtitles hash sums.
const oshashBlockBytes = 65536

// OSHash is media_object.oshash: the OpenSubtitles hash, the file size plus the 64-bit words of its first and last
// 64 KiB, as 16 hex digits; "" for a file under 64 KiB, which the hash does not define, and for anything but video.
func OSHash(fsys fsx.FS, absolutePath string, size int64) (string, error) {
	if filekind.Of(absolutePath) != filekind.Video || size < oshashBlockBytes {
		return "", nil
	}
	file, err := fsys.Open(absolutePath)
	if err != nil {
		return "", fmt.Errorf("library: oshash %s: %w", absolutePath, err)
	}
	defer func() { _ = file.Close() }()

	sum := uint64(size)
	block := make([]byte, oshashBlockBytes)
	for _, offset := range []int64{0, size - oshashBlockBytes} {
		if _, err := file.ReadAt(block, offset); err != nil {
			return "", fmt.Errorf("library: oshash %s: %w", absolutePath, err)
		}
		for i := 0; i < oshashBlockBytes; i += 8 {
			sum += binary.LittleEndian.Uint64(block[i:])
		}
	}
	return fmt.Sprintf("%016x", sum), nil
}
