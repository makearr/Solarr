package jellyfin_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/mediaserver/jellyfin"
)

func TestFetchImageSignsAndRefusesForeignAddresses(t *testing.T) {
	var seen string
	doer := mediaserver.DoerFunc(func(_ context.Context, request *http.Request) (*http.Response, error) {
		seen = request.Header.Get("Authorization")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("bytes"))}, nil
	})
	client := jellyfin.New(doer, "http://media.invalid:8096", "key-1")
	address := "http://media.invalid:8096/Items/abc/Images/Primary"
	if !client.OwnsImage(address) || client.OwnsImage("http://media.invalid:8096/Users/abc") || client.OwnsImage("http://other.invalid/Items/abc/Images/Primary") {
		t.Fatal("OwnsImage must accept only this server's item pictures")
	}
	got, err := client.FetchImage(context.Background(), address, 1<<20)
	if err != nil || string(got) != "bytes" || !strings.Contains(seen, `Token="key-1"`) {
		t.Fatalf("got %q, %v, header %q", got, err, seen)
	}
	if _, err := client.FetchImage(context.Background(), "http://other.invalid/Items/abc/Images/Primary", 1<<20); err == nil {
		t.Fatal("a foreign address must not be fetched with the token")
	}
}
