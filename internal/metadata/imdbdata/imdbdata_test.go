package imdbdata_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/imdbdata"
	"github.com/makearr/Solarr/internal/metadata/metacache"
)

type memoryStore struct {
	rows   map[string]metacache.ImdbRating
	writes []int
}

func (m *memoryStore) SetImdbRatings(_ context.Context, ratings map[string]metacache.ImdbRating) error {
	if m.rows == nil {
		m.rows = map[string]metacache.ImdbRating{}
	}
	for title, rating := range ratings {
		m.rows[title] = rating
	}
	m.writes = append(m.writes, len(ratings))
	return nil
}

func (m *memoryStore) ImdbRatingOf(_ context.Context, title string) (metacache.ImdbRating, error) {
	if rating, ok := m.rows[title]; ok {
		return rating, nil
	}
	return metacache.ImdbRating{}, metacache.ErrNotFound
}

func (m *memoryStore) ImdbRatingCount(context.Context) (int64, error) { return int64(len(m.rows)), nil }

type fakeDoer struct{ body []byte }

func (f fakeDoer) Get(context.Context, string) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(f.body))}, nil
}

func gzipped(t *testing.T, text string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestSync_StoresEachWellFormedRowInChunksAndSkipsTheRest(t *testing.T) {
	text := "tconst\taverageRating\tnumVotes\ntt0000001\t5.7\t2100\ntt0000002\t0\t5\nbroken line\ntt0000003\t8.4\t1,5\ntt0000004\t8.4\t15\n"
	store := &memoryStore{}
	stored, err := imdbdata.New(fakeDoer{gzipped(t, text)}, store).Sync(t.Context())
	if err != nil || stored != 2 {
		t.Fatalf("Sync = %d, %v; want 2, nil", stored, err)
	}
	want := map[string]metacache.ImdbRating{"tt0000001": {Rating: 5.7, Votes: 2100}, "tt0000004": {Rating: 8.4, Votes: 15}}
	if !reflect.DeepEqual(store.rows, want) {
		t.Errorf("rows = %+v, want %+v", store.rows, want)
	}
}

func TestSync_WritesInChunksOfTwoHundredRows(t *testing.T) {
	var text bytes.Buffer
	for row := 0; row < 450; row++ {
		text.WriteString("tt" + string(rune('a'+row/26/26%26)) + string(rune('a'+row/26%26)) + string(rune('a'+row%26)) + "\t7.0\t10\n")
	}
	store := &memoryStore{}
	stored, err := imdbdata.New(fakeDoer{gzipped(t, text.String())}, store).Sync(t.Context())
	if err != nil || stored != 450 {
		t.Fatalf("Sync = %d, %v; want 450, nil", stored, err)
	}
	if want := []int{200, 200, 50}; !reflect.DeepEqual(store.writes, want) {
		t.Errorf("writes = %v, want %v", store.writes, want)
	}
}

func TestRating_AnswersWhatWasStoredAndNotFoundOtherwise(t *testing.T) {
	store := &memoryStore{rows: map[string]metacache.ImdbRating{"tt0000001": {Rating: 5.7, Votes: 2100}}}
	client := imdbdata.New(fakeDoer{}, store)
	score, votes, err := client.Rating(t.Context(), "tt0000001")
	if score != 5.7 || votes != 2100 || err != nil {
		t.Errorf("Rating = %v, %d, %v; want 5.7, 2100, nil", score, votes, err)
	}
	if _, _, err := client.Rating(t.Context(), "tt9"); !errors.Is(err, imdbdata.ErrNotFound) {
		t.Errorf("unknown title error = %v, want ErrNotFound", err)
	}
}
