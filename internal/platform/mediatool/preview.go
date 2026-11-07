package mediatool

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"math"
)

const (
	// The sheet is sheetColumns x sheetRows cells, each fitted into the cell box: 1278x720 for a 16:9 picture.
	sheetColumns, sheetRows         = 3, 3
	cellBoxWidth, cellBoxHeight     = 426, 240
	singleBoxWidth, singleBoxHeight = 1280, 720
	// singleSlot is the third of nine moments, at three tenths of the video: inside its first third, not at the start.
	singleSlot = 2
	// previewQuality is the JPEG quality of both pictures.
	previewQuality = 80
)

// Preview is the pictures made from the frames a hash was made from, as JPEG; empty where none could be made.
type Preview struct {
	Frame, Sheet []byte
}

// pictureSize is the width and height of a frame in pixels.
type pictureSize struct{ width, height int }

// moment is what was read at one sampled moment: RGB frames of one size, none when it could not be read.
type moment struct {
	size   pictureSize
	frames [][]byte
}

// displaySize is how the picture looks on screen, the pixel shape applied.
func displaySize(measured Measured) pictureSize {
	width, height := measured.Width, measured.Height
	if width <= 0 || height <= 0 {
		return pictureSize{width: cellBoxWidth, height: cellBoxHeight}
	}
	if measured.PixelAspect > 0 {
		width = int(float64(width)*measured.PixelAspect + 0.5)
	}
	return pictureSize{width: width, height: height}
}

// fitted is the size inside the box with the same shape, never larger than the picture, both sides even.
func (p pictureSize) fitted(boxWidth, boxHeight int) pictureSize {
	scale := min(1.0, float64(boxWidth)/float64(p.width), float64(boxHeight)/float64(p.height))
	even := func(value float64) int { return max(2, int(math.Round(value*scale/2))*2) }
	return pictureSize{width: even(float64(p.width)), height: even(float64(p.height))}
}

// greyOf reduces an RGB frame to the FrameSide square of grey the hash works on, stretched and averaged by area.
func greyOf(rgb []byte, size pictureSize) []byte {
	out := make([]byte, FrameSide*FrameSide)
	for y := range FrameSide {
		y0, y1 := y*size.height/FrameSide, (y+1)*size.height/FrameSide
		for x := range FrameSide {
			x0, x1 := x*size.width/FrameSide, (x+1)*size.width/FrameSide
			sum, count := 0, 0
			for row := y0; row < max(y1, y0+1); row++ {
				for column := x0; column < max(x1, x0+1); column++ {
					at := (row*size.width + column) * 3
					sum += 299*int(rgb[at]) + 587*int(rgb[at+1]) + 114*int(rgb[at+2])
					count++
				}
			}
			out[y*FrameSide+x] = byte(sum / (count * 1000))
		}
	}
	return out
}

// resized averages an RGB frame down to a smaller size.
func resized(rgb []byte, from, to pictureSize) []byte {
	if from == to {
		return rgb
	}
	out := make([]byte, to.width*to.height*3)
	for y := range to.height {
		y0, y1 := y*from.height/to.height, (y+1)*from.height/to.height
		for x := range to.width {
			x0, x1 := x*from.width/to.width, (x+1)*from.width/to.width
			var sums [3]int
			count := 0
			for row := y0; row < max(y1, y0+1); row++ {
				for column := x0; column < max(x1, x0+1); column++ {
					at := (row*from.width + column) * 3
					sums[0], sums[1], sums[2] = sums[0]+int(rgb[at]), sums[1]+int(rgb[at+1]), sums[2]+int(rgb[at+2])
					count++
				}
			}
			at := (y*to.width + x) * 3
			out[at], out[at+1], out[at+2] = byte(sums[0]/count), byte(sums[1]/count), byte(sums[2]/count)
		}
	}
	return out
}

// bestFrame is the index of the window's frame with the most picture in it, so a fade or a black frame is passed over.
func bestFrame(grey [][]byte) int {
	best, bestSpread := 0, -1.0
	for index, frame := range grey {
		if spread, _ := frameSpread(frame); spread > bestSpread {
			best, bestSpread = index, spread
		}
	}
	return best
}

// previewOf makes the single frame and the sheet from the frames the hash was made from: each moment's most
// informative frame, in colour and in the video's own shape.
func previewOf(moments []moment, grey [][][]byte, cell pictureSize) Preview {
	sheet := image.NewRGBA(image.Rect(0, 0, cell.width*sheetColumns, cell.height*sheetRows))
	draw.Draw(sheet, sheet.Bounds(), image.Black, image.Point{}, draw.Src)
	picked := make([][]byte, len(moments))
	for slot, one := range moments {
		if len(one.frames) == 0 {
			continue
		}
		picked[slot] = one.frames[bestFrame(grey[slot])]
		if slot < sheetColumns*sheetRows {
			column, row := slot%sheetColumns, slot/sheetColumns
			paste(sheet, image.Pt(column*cell.width, row*cell.height), resized(picked[slot], one.size, cell), cell)
		}
	}
	out := Preview{Sheet: encodeJPEG(sheet)}
	// The single frame is the one at singleSlot, else the nearest earlier moment, then any later one.
	for _, slot := range append([]int{singleSlot, 1, 0}, laterSlots(len(moments))...) {
		if picked[slot] == nil {
			continue
		}
		full := image.NewRGBA(image.Rect(0, 0, moments[slot].size.width, moments[slot].size.height))
		paste(full, image.Point{}, picked[slot], moments[slot].size)
		out.Frame = encodeJPEG(full)
		break
	}
	return out
}

func laterSlots(count int) []int {
	var slots []int
	for slot := singleSlot + 1; slot < count; slot++ {
		slots = append(slots, slot)
	}
	return slots
}

// paste copies an RGB frame into the picture at a position.
func paste(into *image.RGBA, at image.Point, rgb []byte, size pictureSize) {
	for y := range size.height {
		offset := into.PixOffset(at.X, at.Y+y)
		for x := range size.width {
			source := (y*size.width + x) * 3
			into.Pix[offset+x*4], into.Pix[offset+x*4+1], into.Pix[offset+x*4+2], into.Pix[offset+x*4+3] = rgb[source], rgb[source+1], rgb[source+2], 255
		}
	}
}

func encodeJPEG(picture image.Image) []byte {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, picture, &jpeg.Options{Quality: previewQuality}); err != nil {
		return nil
	}
	return out.Bytes()
}
