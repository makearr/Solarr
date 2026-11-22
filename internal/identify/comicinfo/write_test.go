package comicinfo

import (
	"archive/zip"
	"bytes"
	"io"
	"reflect"
	"testing"
)

func TestMergeFillsWhatIsMissingAndReplaceOverwrites(t *testing.T) {
	existing := []byte(`<?xml version="1.0"?><ComicInfo><Series>Old Name</Series><Number>3</Number><Pages><Page Image="0"/></Pages></ComicInfo>`)
	info := Info{Series: "The Lantern", Number: "3", Year: 2031, Publisher: "Dawn Press", Credits: map[string][]string{"writer": {"A. Writer", "B. Writer"}}}

	filled, changed, err := Merge(existing, info, ModeFill)
	if err != nil || !changed {
		t.Fatalf("fill = %v, %v", changed, err)
	}
	got, err := ParseComicInfo(filled)
	if err != nil || got.Series != "Old Name" || got.Year != 2031 || got.Publisher != "Dawn Press" || !reflect.DeepEqual(got.Credits["writer"], []string{"A. Writer", "B. Writer"}) {
		t.Fatalf("fill kept the existing value and added the missing ones: %+v, %v", got, err)
	}
	if !bytes.Contains(filled, []byte(`<Page Image="0"/>`)) {
		t.Fatalf("a structured element survives: %s", filled)
	}
	if again, changed, err := Merge(filled, info, ModeFill); err != nil || changed || !bytes.Equal(again, filled) {
		t.Fatalf("filling a filled file changes nothing: %v %v", changed, err)
	}
	replaced, changed, err := Merge(existing, info, ModeReplace)
	if err != nil || !changed {
		t.Fatalf("replace = %v, %v", changed, err)
	}
	if got, _ := ParseComicInfo(replaced); got.Series != "The Lantern" {
		t.Fatalf("replace overwrites: %+v", got)
	}
}

func TestRewriteCBZKeepsEntriesOrderAndMethod(t *testing.T) {
	var source bytes.Buffer
	writer := zip.NewWriter(&source)
	for _, name := range []string{"001.jpg", "002.jpg"} {
		out, _ := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		_, _ = out.Write([]byte("page " + name))
	}
	_ = writer.Close()

	var rewritten bytes.Buffer
	if err := RewriteCBZ(bytes.NewReader(source.Bytes()), int64(source.Len()), &rewritten, []byte("<ComicInfo/>")); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(rewritten.Bytes()), int64(rewritten.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range reader.File {
		names = append(names, file.Name)
		if file.Name != ComicInfoName && file.Method != zip.Store {
			t.Errorf("%s changed method to %d", file.Name, file.Method)
		}
		body, _ := file.Open()
		text, _ := io.ReadAll(body)
		if file.Name == "001.jpg" && string(text) != "page 001.jpg" {
			t.Errorf("page bytes changed: %q", text)
		}
	}
	if !reflect.DeepEqual(names, []string{"001.jpg", "002.jpg", ComicInfoName}) {
		t.Fatalf("entries = %v", names)
	}
}
