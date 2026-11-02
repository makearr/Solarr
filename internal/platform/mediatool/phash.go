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

// phashSamples is how many frames across a video's length are hashed.
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

// FrameHash is one sampled frame's perceptual hash; Slot is which of the phashSamples moments it was taken at.
type FrameHash struct {
	Slot int
	Hash uint64
}

// VideoHash is a video's perceptual hashes (ADR-35): Mean of the averaged frames finds candidates cheaply, Frames
// decide, since the mean image of one show's episodes is near-identical. A flat frame (black, a fade) is left out.
type VideoHash struct {
	Mean   uint64
	Frames []FrameHash
}

// flatSpread is the grey-level standard deviation under which a frame carries no picture worth comparing.
const flatSpread = 5.0

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
	return dctHash(&mean), nil
}

// HashOfFrames hashes sampled frames, nil for a slot whose frame could not be read, into a VideoHash.
func HashOfFrames(slots [][]byte) (VideoHash, error) {
	var read [][]byte
	var frames []FrameHash
	for slot, frame := range slots {
		if frame == nil {
			continue
		}
		read = append(read, frame)
		if hash, informative := frameHash(frame); informative {
			frames = append(frames, FrameHash{Slot: slot, Hash: hash})
		}
	}
	mean, err := PerceptualHashOfFrames(read)
	if err != nil {
		return VideoHash{}, err
	}
	return VideoHash{Mean: mean, Frames: frames}, nil
}

// frameHash is one frame's hash, and false when the frame is too flat to tell one video from another.
func frameHash(frame []byte) (uint64, bool) {
	var image [phashSide][phashSide]float64
	sum, squares := 0.0, 0.0
	for index, value := range frame {
		grey := float64(value)
		image[index/phashSide][index%phashSide] = grey
		sum += grey
		squares += grey * grey
	}
	count := float64(len(frame))
	spread := math.Sqrt(max(0, squares/count-(sum/count)*(sum/count)))
	return dctHash(&image), spread >= flatSpread
}

func dctHash(image *[phashSide][phashSide]float64) uint64 {
	var rows, coefficients [phashSide][phashSide]float64
	for y := range phashSide {
		for k := range 8 {
			for x := range phashSide {
				rows[y][k] += image[y][x] * dctBasis[k][x]
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
	return hash
}

// PerceptualDistance is the number of bits two perceptual hashes differ in; under about 10 of 64 is the same video.
func PerceptualDistance(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// PerceptualHash samples the video at phashSamples evenly spaced moments and hashes the frames (ADR-35).
func (s *Subprocess) PerceptualHash(ctx context.Context, path string) (VideoHash, error) {
	probe := s.probeOne(ctx, path)
	if probe.Err != nil {
		return VideoHash{}, probe.Err
	}
	length := probe.Measured.Duration
	if length <= 0 {
		return VideoHash{}, ErrNoFrames
	}
	slots := make([][]byte, phashSamples)
	read := 0
	for sample := range phashSamples {
		at := length * time.Duration(sample+1) / (phashSamples + 1)
		frame, err := s.frameAt(ctx, path, at)
		if err != nil {
			if ctx.Err() != nil {
				return VideoHash{}, ctx.Err()
			}
			continue
		}
		slots[sample] = frame
		read++
	}
	if read <= phashSamples/2 {
		return VideoHash{}, ErrNoFrames
	}
	return HashOfFrames(slots)
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
