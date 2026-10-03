package remoteimage_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/remoteimage"
)

type fakeHTTP struct {
	body   []byte
	status int
	asked  []string
}

func (f *fakeHTTP) Get(_ context.Context, address string) (*http.Response, error) {
	f.asked = append(f.asked, address)
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(f.body))}, nil
}

func picture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 300, 450))
	for x := 0; x < 300; x++ {
		for y := 0; y < 450; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func newCache(t *testing.T, fake *fakeHTTP) (*remoteimage.Cache, *fsx.Mem) {
	t.Helper()
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	return &remoteimage.Cache{HTTP: fake, FS: fsys, Dir: "/cache/images", Hosts: remoteimage.DefaultHosts}, fsys
}

func TestAPictureIsFetchedOnceAndStoredAtEverySize(t *testing.T) {
	fake := &fakeHTTP{body: picture(t)}
	cache, fsys := newCache(t, fake)
	address := "https://image.tmdb.org/t/p/original/abc.jpg"
	key, err := cache.Ensure(t.Context(), address)
	if err != nil {
		t.Fatal(err)
	}
	again, err := cache.Ensure(t.Context(), address)
	if err != nil || again != key || len(fake.asked) != 1 {
		t.Fatalf("second ensure = %q, %v after %d fetches; want the cached one", again, err, len(fake.asked))
	}
	for _, size := range []string{"thumb", "small", "full"} {
		if _, err := fsys.Stat("/cache/images/" + key + "-" + size + ".webp"); err != nil {
			t.Errorf("%s not stored: %v", size, err)
		}
	}
	if !strings.HasPrefix(key, "remote-") || key != remoteimage.Key(address) {
		t.Errorf("key = %q", key)
	}
}

func TestOnlyTheProvidersHttpsImageHostsAreFetched(t *testing.T) {
	fake := &fakeHTTP{body: picture(t)}
	cache, _ := newCache(t, fake)
	for _, address := range []string{
		"http://image.tmdb.org/t/p/x.jpg", "https://evil.example/x.jpg", "https://image.tmdb.org.evil.example/x.jpg",
		"https://user@image.tmdb.org/x.jpg", "file:///etc/passwd", "http://10.0.0.1/x.jpg",
	} {
		if _, err := cache.Ensure(t.Context(), address); !errors.Is(err, remoteimage.ErrNotAllowed) {
			t.Errorf("%s: err = %v, want ErrNotAllowed", address, err)
		}
	}
	if len(fake.asked) != 0 {
		t.Errorf("fetched %v", fake.asked)
	}
	if _, err := cache.Ensure(t.Context(), "https://artworks.thetvdb.com/banners/posters/1-4.jpg"); err != nil {
		t.Errorf("a subdomain of an allowed host: %v", err)
	}
}

func TestAProviderErrorOrANonImageIsUnavailable(t *testing.T) {
	for _, fake := range []*fakeHTTP{{status: http.StatusNotFound}, {body: []byte("not a picture")}} {
		cache, _ := newCache(t, fake)
		if _, err := cache.Ensure(t.Context(), "https://image.tmdb.org/t/p/original/x.jpg"); !errors.Is(err, remoteimage.ErrUnavailable) {
			t.Errorf("err = %v, want ErrUnavailable", err)
		}
	}
}

type pathHTTP struct {
	fakeHTTP
	refuse string
}

func (f *pathHTTP) Get(ctx context.Context, address string) (*http.Response, error) {
	if strings.Contains(address, f.refuse) {
		f.asked = append(f.asked, address)
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	return f.fakeHTTP.Get(ctx, address)
}

func TestATMDBPictureIsFetchedSmallAndFullOnlyWhenTheSmallOneIsNotThere(t *testing.T) {
	smallOnly := &fakeHTTP{body: picture(t)}
	cache, _ := newCache(t, smallOnly)
	address := "https://image.tmdb.org/t/p/original/abc.jpg"
	if _, err := cache.Ensure(t.Context(), address); err != nil || len(smallOnly.asked) != 1 || !strings.Contains(smallOnly.asked[0], "/t/p/w500/") {
		t.Fatalf("asked %v, %v; want the 500-pixel rendition alone", smallOnly.asked, err)
	}
	if !cache.Cached(address) {
		t.Error("a stored picture is not reported cached")
	}

	profile := &pathHTTP{fakeHTTP: fakeHTTP{body: picture(t)}, refuse: "/t/p/w500/"}
	cache, _ = newCache(t, &profile.fakeHTTP)
	cache.HTTP = profile
	if _, err := cache.Ensure(t.Context(), address); err != nil || len(profile.asked) != 2 || !strings.Contains(profile.asked[1], "/t/p/original/") {
		t.Fatalf("asked %v, %v; want the original after the small one was refused", profile.asked, err)
	}
}
