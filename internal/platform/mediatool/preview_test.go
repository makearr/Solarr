package mediatool

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

func TestPictureSize_FittedKeepsTheShapeAndNeverGrows(t *testing.T) {
	cases := []struct {
		name       string
		picture    pictureSize
		boxW, boxH int
		want       pictureSize
	}{
		{"1080p into the cell box", pictureSize{1920, 1080}, cellBoxWidth, cellBoxHeight, pictureSize{426, 240}},
		{"scope keeps its shape", pictureSize{1920, 800}, cellBoxWidth, cellBoxHeight, pictureSize{426, 178}},
		{"small stays", pictureSize{320, 180}, cellBoxWidth, cellBoxHeight, pictureSize{320, 180}},
		{"4:3", pictureSize{640, 480}, singleBoxWidth, singleBoxHeight, pictureSize{640, 480}},
	}
	for _, test := range cases {
		if got := test.picture.fitted(test.boxW, test.boxH); got != test.want {
			t.Errorf("%s: %v, want %v", test.name, got, test.want)
		}
	}
}

func TestDisplaySize_AppliesThePixelShape(t *testing.T) {
	if got := displaySize(Measured{Width: 720, Height: 576, PixelAspect: 16.0 / 11}); got != (pictureSize{1047, 576}) {
		t.Errorf("anamorphic PAL = %v, want 1047x576", got)
	}
	if got := displaySize(Measured{Width: 1280, Height: 720}); got != (pictureSize{1280, 720}) {
		t.Errorf("square pixels = %v", got)
	}
}

func solidRGB(size pictureSize, red, green, blue byte) []byte {
	out := make([]byte, size.width*size.height*3)
	for at := 0; at < len(out); at += 3 {
		out[at], out[at+1], out[at+2] = red, green, blue
	}
	return out
}

func TestGreyOf_IsLumaAtFrameSide(t *testing.T) {
	size := pictureSize{426, 240}
	grey := greyOf(solidRGB(size, 255, 0, 0), size)
	if len(grey) != FrameSide*FrameSide || grey[0] != 76 {
		t.Errorf("grey of pure red = %d bytes starting %d, want %d starting 76", len(grey), grey[0], FrameSide*FrameSide)
	}
}

func TestPreviewOf_SheetIsThreeByThreeAndSingleIsFromTheFirstThird(t *testing.T) {
	cell := pictureSize{64, 36}
	moments := make([]moment, phashSamples)
	grey := make([][][]byte, phashSamples)
	for slot := range moments {
		frame := solidRGB(cell, byte(slot*25), 0, 0)
		moments[slot] = moment{size: cell, frames: [][]byte{frame}}
		grey[slot] = [][]byte{greyOf(frame, cell)}
	}
	preview := previewOf(moments, grey, cell)
	sheet, err := jpeg.Decode(bytes.NewReader(preview.Sheet))
	if err != nil || sheet.Bounds() != image.Rect(0, 0, 192, 108) {
		t.Fatalf("sheet %v %v, want 192x108", sheet.Bounds(), err)
	}
	frame, err := jpeg.Decode(bytes.NewReader(preview.Frame))
	if err != nil || frame.Bounds() != image.Rect(0, 0, 64, 36) {
		t.Fatalf("frame %v %v, want 64x36", frame.Bounds(), err)
	}
	if red, _, _, _ := frame.At(10, 10).RGBA(); red>>8 < 40 || red>>8 > 60 {
		t.Errorf("single frame red = %d, want about 50, the third moment's", red>>8)
	}
}

func TestPreviewOf_UnreadMomentsLeaveBlackCellsAndASingleFromTheNearest(t *testing.T) {
	cell := pictureSize{64, 36}
	moments := make([]moment, phashSamples)
	grey := make([][][]byte, phashSamples)
	for _, slot := range []int{0, 1, 4, 5, 6} {
		frame := solidRGB(cell, 200, 100, 50)
		moments[slot] = moment{size: cell, frames: [][]byte{frame}}
		grey[slot] = [][]byte{greyOf(frame, cell)}
	}
	preview := previewOf(moments, grey, cell)
	if len(preview.Sheet) == 0 || len(preview.Frame) == 0 {
		t.Fatalf("sheet %d bytes, frame %d bytes; want both", len(preview.Sheet), len(preview.Frame))
	}
}
