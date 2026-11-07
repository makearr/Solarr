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

// FrameSide is the width and height ffmpeg delivers each frame at, stretched to a square so aspect ratio and pixel shape
// drop out; the black bars are cropped from it before the reduction to phashSide.
const FrameSide = 128

// PerceptualVersion stamps the algorithm in the stored hashes; bump it when hashes stop being comparable.
const PerceptualVersion = 3

const (
	// phashSamples is how many moments across a video's length are hashed, at stable fractions of its duration.
	phashSamples = 9
	// windowFrames frames, windowStep apart, are read at each moment, so a shift of a few frames still lands on one of them.
	windowFrames = 5
	windowRate   = 8
	windowStep   = time.Second / windowRate
)

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

// FrameHash is one frame's perceptual hash; Slot is which of the phashSamples moments it was taken at, and a moment
// holds the hashes of a few neighbouring frames.
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

// A row or column is a black bar when barShare of it is at most barLevel grey, so a line of subtitles set in a bar
// leaves it a bar; barLimit is the share of a side a bar may take, so a dark scene is never cropped away.
const (
	barLevel = 40
	barShare = 0.55
	barLimit = FrameSide * 35 / 100
)

// cutJump is the mean grey change between two neighbouring frames of a window over which a scene changed.
const cutJump = 28.0

type bars struct{ top, bottom, left, right int }

// lineIsBar is whether the FrameSide values at offset + i*step are mostly black.
func lineIsBar(frame []byte, offset, step int) bool {
	dark := 0
	for i := range FrameSide {
		if frame[offset+i*step] <= barLevel {
			dark++
		}
	}
	return float64(dark) >= barShare*FrameSide
}

func barsOf(frame []byte) bars {
	var found bars
	for found.top < barLimit && lineIsBar(frame, found.top*FrameSide, 1) {
		found.top++
	}
	for found.bottom < barLimit && lineIsBar(frame, (FrameSide-1-found.bottom)*FrameSide, 1) {
		found.bottom++
	}
	for found.left < barLimit && lineIsBar(frame, found.left, FrameSide) {
		found.left++
	}
	for found.right < barLimit && lineIsBar(frame, FrameSide-1-found.right, FrameSide) {
		found.right++
	}
	return found
}

// commonBars is the bars most of the video's frames show: the lower quartile per side, so a subtitle set in a bar or a
// dark scene on a few frames neither shrinks nor grows the crop.
func commonBars(frames [][]byte) bars {
	var tops, bottoms, lefts, rights []int
	for _, frame := range frames {
		if _, informative := frameSpread(frame); !informative {
			continue
		}
		found := barsOf(frame)
		tops, bottoms = append(tops, found.top), append(bottoms, found.bottom)
		lefts, rights = append(lefts, found.left), append(rights, found.right)
	}
	quartile := func(values []int) int {
		if len(values) == 0 {
			return 0
		}
		slices.Sort(values)
		if bar := values[len(values)/4]; bar > 0 {
			return bar + 1
		}
		return 0
	}
	return bars{top: quartile(tops), bottom: quartile(bottoms), left: quartile(lefts), right: quartile(rights)}
}

// thumbnail crops the bars off a FrameSide frame and reduces what is left to phashSide x phashSide by area average.
func thumbnail(frame []byte, crop bars) []byte {
	left, right := crop.left, FrameSide-crop.right
	top, bottom := crop.top, FrameSide-crop.bottom
	out := make([]byte, phashSide*phashSide)
	for y := range phashSide {
		y0, y1 := top+y*(bottom-top)/phashSide, top+(y+1)*(bottom-top)/phashSide
		for x := range phashSide {
			x0, x1 := left+x*(right-left)/phashSide, left+(x+1)*(right-left)/phashSide
			sum, count := 0, 0
			for row := y0; row < max(y1, y0+1); row++ {
				for column := x0; column < max(x1, x0+1); column++ {
					sum += int(frame[row*FrameSide+column])
					count++
				}
			}
			out[y*phashSide+x] = byte(sum / count)
		}
	}
	return out
}

// longestRun is the longest stretch of a window with no scene change inside it, the earliest on a tie.
func longestRun(thumbnails [][]byte) [][]byte {
	bestStart, bestLength, start := 0, 0, 0
	for i := 1; i <= len(thumbnails); i++ {
		if i < len(thumbnails) && meanChange(thumbnails[i-1], thumbnails[i]) <= cutJump {
			continue
		}
		if i-start > bestLength {
			bestStart, bestLength = start, i-start
		}
		start = i
	}
	return thumbnails[bestStart : bestStart+bestLength]
}

func meanChange(a, b []byte) float64 {
	total := 0
	for index := range a {
		total += max(int(a[index])-int(b[index]), int(b[index])-int(a[index]))
	}
	return float64(total) / float64(len(a))
}

// HashOfWindows hashes what was read at each sampled moment: windows[slot] holds FrameSide frames in time order, empty
// for a moment that could not be read. Black bars are cropped by what the whole video shows, each frame is reduced to
// 32x32 grey, a window keeps only its longest run without a scene change, and each kept frame gets a DCT hash.
func HashOfWindows(windows [][][]byte) (VideoHash, error) {
	var all [][]byte
	for _, window := range windows {
		for _, frame := range window {
			if len(frame) != FrameSide*FrameSide {
				return VideoHash{}, fmt.Errorf("mediatool: frame of %d bytes, want %d", len(frame), FrameSide*FrameSide)
			}
			all = append(all, frame)
		}
	}
	if len(all) == 0 {
		return VideoHash{}, ErrNoFrames
	}
	crop := commonBars(all)
	var frames []FrameHash
	var kept [][]byte
	for slot, window := range windows {
		thumbnails := make([][]byte, len(window))
		for index, frame := range window {
			thumbnails[index] = thumbnail(frame, crop)
		}
		var previous uint64
		for index, picture := range longestRun(thumbnails) {
			hash, informative := frameHash(picture)
			if !informative {
				continue
			}
			kept = append(kept, picture)
			if index > 0 && hash == previous {
				continue
			}
			previous = hash
			frames = append(frames, FrameHash{Slot: slot, Hash: hash})
		}
	}
	if len(kept) == 0 {
		return VideoHash{}, ErrNoFrames
	}
	mean, err := PerceptualHashOfFrames(kept)
	if err != nil {
		return VideoHash{}, err
	}
	return VideoHash{Mean: mean, Frames: frames}, nil
}

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

// frameSpread is a frame's grey standard deviation, and false when it is too flat to tell one video from another.
func frameSpread(frame []byte) (float64, bool) {
	sum, squares := 0.0, 0.0
	for _, value := range frame {
		grey := float64(value)
		sum += grey
		squares += grey * grey
	}
	count := float64(len(frame))
	spread := math.Sqrt(max(0, squares/count-(sum/count)*(sum/count)))
	return spread, spread >= flatSpread
}

// frameHash is one thumbnail's hash, and false when the frame is too flat to tell one video from another.
func frameHash(frame []byte) (uint64, bool) {
	var image [phashSide][phashSide]float64
	for index, value := range frame {
		image[index/phashSide][index%phashSide] = float64(value)
	}
	_, informative := frameSpread(frame)
	return dctHash(&image), informative
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

// PerceptualHash reads a short window at phashSamples stable fractions of the video's duration, so another frame rate,
// codec, size or aspect ratio gives the same pictures, and hashes them (ADR-35, documentation/duplicate-videos.md).
func (s *Subprocess) PerceptualHash(ctx context.Context, path string) (VideoHash, error) {
	hash, _, err := s.HashAndPreview(ctx, path, false)
	return hash, err
}

// HashAndPreview reads each sampled moment once, in colour at the size the preview wants, and makes both the perceptual
// hash and, when previews is set, the single frame and the thumbnail sheet from those same frames.
func (s *Subprocess) HashAndPreview(ctx context.Context, path string, previews bool) (VideoHash, Preview, error) {
	probe := s.probeOne(ctx, path)
	if probe.Err != nil {
		return VideoHash{}, Preview{}, probe.Err
	}
	length := probe.Measured.Duration
	if length <= 0 {
		return VideoHash{}, Preview{}, ErrNoFrames
	}
	display := displaySize(probe.Measured)
	cell := display.fitted(cellBoxWidth, cellBoxHeight)
	moments := make([]moment, phashSamples)
	read := 0
	for sample := range phashSamples {
		at := length * time.Duration(sample+1) / (phashSamples + 1)
		size := cell
		if previews && sample == singleSlot {
			size = display.fitted(singleBoxWidth, singleBoxHeight)
		}
		frames, err := s.windowAt(ctx, path, at, size)
		if err != nil {
			if ctx.Err() != nil {
				return VideoHash{}, Preview{}, ctx.Err()
			}
			continue
		}
		moments[sample] = moment{size: size, frames: frames}
		read++
	}
	if read <= phashSamples/2 {
		return VideoHash{}, Preview{}, ErrNoFrames
	}
	windows := make([][][]byte, phashSamples)
	for slot, one := range moments {
		for _, frame := range one.frames {
			windows[slot] = append(windows[slot], greyOf(frame, one.size))
		}
	}
	hash, err := HashOfWindows(windows)
	if err != nil || !previews {
		return hash, Preview{}, err
	}
	return hash, previewOf(moments, windows, cell), nil
}

// windowAt reads windowFrames frames windowStep apart, centred on at, as RGB at size; the fps filter makes the spacing
// the same whatever the source's frame rate, and the sample aspect ratio is applied before the picture is resized.
func (s *Subprocess) windowAt(ctx context.Context, path string, at time.Duration, size pictureSize) ([][]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()

	start := max(0, at-windowStep*windowFrames/2)
	args := []string{"-v", "error", "-protocol_whitelist", "file"}
	rest := []string{"-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64), "-i", path,
		"-an", "-sn", "-frames:v", strconv.Itoa(windowFrames),
		"-vf", fmt.Sprintf("fps=%d,scale=trunc(iw*sar):ih,setsar=1,scale=%d:%d:flags=area,format=rgb24", windowRate, size.width, size.height),
		"-f", "rawvideo", "pipe:1"}
	bytesPerFrame := size.width * size.height * 3
	valid := func(output []byte) bool { return len(output) > 0 && len(output)%bytesPerFrame == 0 }
	var output []byte
	var err error
	if s.decodeArgs != nil {
		if hardware := s.decodeArgs(); len(hardware) > 0 {
			output, err = s.runner.Run(bounded, ffmpegBinary, slices.Concat(args, hardware, rest)...)
			if err != nil || !valid(output) {
				output, err = nil, errors.New("hardware decode failed")
			}
		}
	}
	if output == nil {
		output, err = s.runner.Run(bounded, ffmpegBinary, slices.Concat(args, rest)...)
	}
	if err != nil {
		return nil, fmt.Errorf("mediatool: window %s: %w", path, err)
	}
	if !valid(output) {
		return nil, fmt.Errorf("mediatool: window %s: %d bytes", path, len(output))
	}
	frames := make([][]byte, 0, len(output)/bytesPerFrame)
	for offset := 0; offset < len(output); offset += bytesPerFrame {
		frames = append(frames, output[offset:offset+bytesPerFrame])
	}
	return frames, nil
}
