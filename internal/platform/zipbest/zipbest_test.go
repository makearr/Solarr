package zipbest_test

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/zipbest"
)

func TestNewWriterDeflatesAtTheBestLevel(t *testing.T) {
	body := strings.Repeat("a page of nearly nothing ", 4000)
	var out bytes.Buffer
	writer := zipbest.NewWriter(&out)
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: "page.xml", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte(body))
	_ = writer.Close()
	reader, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	file := reader.File[0]
	if file.Method != zip.Deflate || file.CompressedSize64 >= uint64(len(body))/50 {
		t.Errorf("method %d, %d of %d bytes: not deflated hard", file.Method, file.CompressedSize64, len(body))
	}
	// Level 9 on repetitive text is smaller than the default level's output for the same text.
	var plain bytes.Buffer
	ordinary := zip.NewWriter(&plain)
	ordinaryEntry, _ := ordinary.CreateHeader(&zip.FileHeader{Name: "page.xml", Method: zip.Deflate})
	_, _ = ordinaryEntry.Write([]byte(body))
	_ = ordinary.Close()
	if out.Len() > plain.Len() {
		t.Errorf("best level wrote %d bytes, default %d", out.Len(), plain.Len())
	}
}
