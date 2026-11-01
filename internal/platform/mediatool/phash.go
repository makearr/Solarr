package mediatool

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"slices"
	"strconv"
	"time"
)

// phashSide is the width and height of the grey thumbnail each sampled frame is reduced to.
const phashSide = 32

// phashSamples is how many frames across a video's length are averaged into one hash.
const phashSamples = 7

// ErrNoFrames means the video gave too few frames to hash: a file ffmpeg cannot decode, or one with no video stream.
var ErrNoFrames = errors.New("mediatool: no usable video frames")

var dctBasis = func() [phashSide][phashSide]float64 {
	var basis [phashSide][phashSide]float64
	for k := range phashSide {
		for n := range phashSide {
			basis[k][n] = math.Cos(math.Pi / phashSide * (float64(n) + 0.5) * float64(k))
		}
	}
	return basis
}()

// PerceptualHashOfFrames hashes grey phashSide x phashSide frames: their mean image, a 2-D DCT, and one bit per
// low-frequency coefficient against the median. The same cut re-encoded, rescaled or re-graded lands within a few bits.
func PerceptualHashOfFrames(frames [][]byte) (uint64, error) {
	if len(frames) == 0 {
		return 0, ErrNoFrames
	}
	var mean [phashSide][phashSide]float64
	for _, frame := range frames {
		if len(frame) != phashSide*phashSide {
			return 0, fmt.Errorf("mediatool: frame of %d bytes, want %d", len(frame), phashSide*phashSide)
		}
		for index, value := range frame {
			mean[index/phashSide][index%phashSide] += float64(value) / float64(len(frames))
		}
	}
	var rows, coefficients [phashSide][phashSide]float64
	for y := range phashSide {
		for k := range 8 {
			for x := range phashSide {
				rows[y][k] += mean[y][x] * dctBasis[k][x]
			}
		}
	}
	for k := range 8 {
		for l := range 8 {
			for y := range phashSide {
				coefficients[l][k] += rows[y][k] * dctBasis[l][y]
			}
		}
	}
	low := make([]float64, 0, 64)
	for l := range 8 {
		low = append(low, coefficients[l][:8]...)
	}
	ac := slices.Clone(low[1:])
	slices.Sort(ac)
	median := ac[len(ac)/2]
	var hash uint64
	for index, value := range low {
		if value > median {
			hash |= 1 << index
		}
	}
	return hash, nil
}

// PerceptualDistance is the number of bits two perceptual hashes differ in; under about 10 of 64 is the same video.
func PerceptualDistance(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// PerceptualHash samples the video at phashSamples evenly spaced moments and hashes the frames (ADR-35).
func (s *Subprocess) PerceptualHash(ctx context.Context, path string) (uint64, error) {
	probe := s.probeOne(ctx, path)
	if probe.Err != nil {
		return 0, probe.Err
	}
	length := probe.Measured.Duration
	if length <= 0 {
		return 0, ErrNoFrames
	}
	var frames [][]byte
	for sample := 1; sample <= phashSamples; sample++ {
		at := length * time.Duration(sample) / (phashSamples + 1)
		frame, err := s.frameAt(ctx, path, at)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			continue
		}
		frames = append(frames, frame)
	}
	if len(frames) <= phashSamples/2 {
		return 0, ErrNoFrames
	}
	return PerceptualHashOfFrames(frames)
}

func (s *Subprocess) frameAt(ctx context.Context, path string, at time.Duration) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()

	output, err := s.runner.Run(bounded, ffmpegBinary,
		"-v", "error", "-protocol_whitelist", "file", "-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64), "-i", path,
		"-an", "-sn", "-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:%d:flags=area,format=gray", phashSide, phashSide),
		"-f", "rawvideo", "pipe:1")
	if err != nil {
		return nil, fmt.Errorf("mediatool: frame %s: %w", path, err)
	}
	if len(output) != phashSide*phashSide {
		return nil, fmt.Errorf("mediatool: frame %s: %d bytes", path, len(output))
	}
	return output, nil
}
