package mediatool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AudioFingerprint is a file's Chromaprint as the 32-bit values fpcalc reports with -raw: what two recordings are
// compared by, which the compressed form AcoustID takes cannot be.
type AudioFingerprint struct {
	Values   []uint32
	Duration time.Duration
}

// AudioFingerprint reads the first two minutes of one audio file as raw Chromaprint values.
func (s *Subprocess) AudioFingerprint(ctx context.Context, path string) (AudioFingerprint, error) {
	if err := s.runner.Lookup(fpcalcBinary); err != nil {
		return AudioFingerprint{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()
	output, runErr := s.runner.Run(bounded, fpcalcBinary, "-length", fpcalcSeconds, "-raw", "-json", path)
	var decoded struct {
		Duration    float64  `json:"duration"`
		Fingerprint []uint32 `json:"fingerprint"`
	}
	// A file fpcalc only partly decoded still prints its fingerprint, which is kept as the AcoustID path keeps it.
	if err := json.Unmarshal(output, &decoded); err != nil || len(decoded.Fingerprint) == 0 {
		if runErr != nil {
			return AudioFingerprint{}, fmt.Errorf("mediatool: fingerprint %s: %w", path, runErr)
		}
		return AudioFingerprint{}, fmt.Errorf("mediatool: fingerprint %s: fpcalc returned none", path)
	}
	return AudioFingerprint{Values: decoded.Fingerprint, Duration: durationFromSeconds(decoded.Duration)}, nil
}
