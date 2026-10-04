package adapters_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/settings"
)

func TestClientCategories_IsOnePerMediaTypeUnlessTheClientNamesItsOwn(t *testing.T) {
	libraries := []string{"music", "tv", "tv"}
	cases := []struct {
		name    string
		options settings.DownloaderOptions
		types   []string
		want    []string
	}{
		{"each media type under its own name", settings.DownloaderOptions{}, libraries, []string{"music", "tv"}},
		{"the client's own category for everything", settings.DownloaderOptions{Category: "solarr"}, libraries, []string{"solarr"}},
		{"no library, nothing to check", settings.DownloaderOptions{}, nil, nil},
	}
	for _, test := range cases {
		if got := adapters.ClientCategories(test.options, test.types); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: ClientCategories = %v, want %v", test.name, got, test.want)
		}
	}
}
