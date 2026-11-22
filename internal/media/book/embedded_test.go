package book

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/identify/bookmeta"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/iogate"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

func TestWithEmbedded_ACalibreSidecarsIdsAndTitleLeadTheNamesReading(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/lib/Mara Quell/Invented Tale", 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create("/lib/Mara Quell/Invented Tale/metadata.opf")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte(`<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>Invented Tale</dc:title>` +
		`<dc:creator>Mara Quell</dc:creator><dc:identifier>9780306406157</dc:identifier></metadata></package>`))
	_ = file.Close()

	named := parsebook.Info{Author: "Invented Tale", Title: "Mara Quell"}
	got := withEmbedded(context.Background(), fsys, "/lib/Mara Quell/Invented Tale/Invented Tale - Mara Quell.epub", named)
	want := parsebook.Info{
		Author: "Mara Quell", Title: "Invented Tale", Titles: []string{"Invented Tale", "Mara Quell"}, ISBN: "9780306406157", TitleUnbacked: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("withEmbedded = %+v, want %+v", got, want)
	}
	if got := withEmbedded(context.Background(), fsys, "/lib/Elsewhere/book.epub", named); !reflect.DeepEqual(got, named) {
		t.Errorf("a book with no metadata = %+v, want its name's reading %+v", got, named)
	}
	if got := withEmbedded(context.Background(), nil, "/x", named); !reflect.DeepEqual(got, named) {
		t.Errorf("no filesystem = %+v, want the name's reading", got)
	}
}

func TestWithTags_AnAudiobooksTagsLeadTheFolderNamesReading(t *testing.T) {
	named := parsebook.Info{Author: "Folder", Title: "Rip 2019"}
	tags := mediatool.Tags{"album": "Invented Tale", "album_artist": "Mara Quell", "artist": "Narrator Person", "ASIN": "b01abc1234", "isbn": "9780306406157"}
	got := withTags(named, tags)
	want := parsebook.Info{
		Author: "Mara Quell", Title: "Invented Tale", Titles: []string{"Invented Tale", "Rip 2019"}, ISBN: "9780306406157", ASIN: "B01ABC1234", TitleUnbacked: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("withTags = %+v, want %+v", got, want)
	}
	if got := withTags(named, mediatool.Tags{"isbn": "1234567890123", "genre": "Audiobook"}); !reflect.DeepEqual(got, named) {
		t.Errorf("tags naming nothing usable = %+v, want the name's reading", got)
	}
	if got := withTags(named, nil); !reflect.DeepEqual(got, named) {
		t.Errorf("no tags = %+v, want the name's reading", got)
	}
}

type recordedTurns struct{ taken [][]string }

func (r *recordedTurns) AcquirePaths(_ context.Context, _ string, paths []string) (func(), error) {
	r.taken = append(r.taken, paths)
	return func() {}, nil
}

func TestWithEmbedded_ReadsTheFileInATurnOnItsDisk(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	_ = fsys.MkdirAll("/lib/A/B", 0o755)
	file, _ := fsys.Create("/lib/A/B/metadata.opf")
	_, _ = file.Write([]byte(`<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>Invented Tale</dc:title></metadata></package>`))
	_ = file.Close()
	turns := &recordedTurns{}
	ctx := iogate.WithTurns(context.Background(), turns)
	withEmbedded(ctx, fsys, "/lib/A/B/B.epub", parsebook.Info{Title: "Invented Tale"})
	if !reflect.DeepEqual(turns.taken, [][]string{{"/lib/A/B/B.epub"}}) {
		t.Errorf("turns taken = %v", turns.taken)
	}
}

func TestLayered_ATitleNoReadingOfTheNameSaysIsUnbackedAnAgreeingOneIsNot(t *testing.T) {
	named := parsebook.Info{Title: "Invented Tale", Author: "Mara Quell"}
	if got := layered(named, bookmeta.Meta{Title: "Something Else"}); !got.TitleUnbacked {
		t.Errorf("a title no reading agrees with: %+v", got)
	}
	if got := layered(named, bookmeta.Meta{Title: "invented tale!"}); got.TitleUnbacked {
		t.Errorf("a title the name reads too: %+v", got)
	}
	if got := layered(parsebook.Info{}, bookmeta.Meta{Title: "Anything"}); got.TitleUnbacked {
		t.Errorf("a name with no reading: %+v", got)
	}
}
