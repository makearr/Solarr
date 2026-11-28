package hardcover

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type answering string

func (a answering) Do(_ context.Context, _ *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(a)))}, nil
}

func TestSeriesBooks_ListsTheBiggestSeriesOfThatNameInOrderAndOnceEach(t *testing.T) {
	client := New(answering(`{"data":{"series":[
		{"book_series":[{"position":1,"book":{"id":9,"title":"Side Story","release_year":2001}}]},
		{"book_series":[
			{"position":1,"book":{"id":1,"title":"First","release_year":2010}},
			{"position":2,"book":{"id":2,"title":"Second","release_year":2011}},
			{"position":2,"book":{"id":2,"title":"Second","release_year":2011}},
			{"position":null,"book":{"id":3,"title":"Extra","release_year":0}}]}]}}`), func() string { return "token" })
	books, err := client.SeriesBooks(t.Context(), "A Series")
	if err != nil {
		t.Fatal(err)
	}
	want := []SeriesBook{{ID: 1, Title: "First", Year: 2010, Position: 1}, {ID: 2, Title: "Second", Year: 2011, Position: 2}, {ID: 3, Title: "Extra"}}
	if len(books) != len(want) {
		t.Fatalf("books = %+v, want %+v", books, want)
	}
	for index := range want {
		if books[index] != want[index] {
			t.Errorf("book %d = %+v, want %+v", index, books[index], want[index])
		}
	}
}

func TestSeriesBooks_AsksNothingWithoutAKey(t *testing.T) {
	books, err := New(answering(`{}`), func() string { return "" }).SeriesBooks(t.Context(), "A Series")
	if books != nil || err != nil {
		t.Errorf("SeriesBooks without a key = %v, %v; want nothing", books, err)
	}
}
