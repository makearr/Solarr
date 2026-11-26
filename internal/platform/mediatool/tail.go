package mediatool

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// tailSeconds is how much of the end of a file is decoded to prove it was not cut short.
const tailSeconds = 8

// tailMinimum is the shortest file whose end is checked; a shorter one is read whole by any player.
const tailMinimum = 30 * time.Second

// DecodeTail decodes the last seconds of a file and answers why that failed; a truncated or damaged file stops there.
func (s *Subprocess) DecodeTail(ctx context.Context, path string, duration time.Duration) error {
	if duration < tailMinimum {
		return nil
	}
	if err := s.runner.Lookup(ffmpegBinary); err != nil {
		return nil //nolint:nilerr // Without ffmpeg nothing can be said of the end of a file.
	}
	bounded, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()
	start := strconv.FormatFloat((duration - tailSeconds*time.Second).Seconds(), 'f', 1, 64)
	if _, err := s.runner.Run(bounded, ffmpegBinary, "-v", "error", "-xerror", "-nostdin", "-ss", start, "-i", path, "-t", strconv.Itoa(tailSeconds), "-f", "null", "-"); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("decode the end of the file: %w", err)
	}
	return nil
}
