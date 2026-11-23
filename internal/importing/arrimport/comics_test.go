package arrimport_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/arrimport"
)

func TestKapowarrReadsVolumesWithTheirRootFolderAndSendsTheKeyAsAQueryParameter(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://kapowarr.local/api/rootfolder?api_key=k1": {200, `{"error":null,"result":[{"id":1,"folder":"/comics"}]}`},
		"http://kapowarr.local/api/volumes?api_key=k1": {200, `{"error":null,"result":[
			{"id":4,"comicvine_id":18001,"title":"Night Riders","year":2019,"monitored":true,"root_folder":1,"folder":"/comics/Night Riders (2019)","issues_downloaded":3},
			{"id":5,"comicvine_id":0,"title":"Old Tales","year":1990,"monitored":false,"root_folder":9,"folder":"/other/Old Tales (1990)","issues_downloaded":0}]}`},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://kapowarr.local/", "k1").Kapowarr()
	got, err := client.Volumes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []arrimport.ComicSeries{
		{ID: "4", Title: "Night Riders", Year: 2019, ComicVine: "18001", Monitored: true, HasFiles: true, Folder: "/comics/Night Riders (2019)", RootFolder: "/comics"},
		{ID: "5", Title: "Old Tales", Year: 1990, Folder: "/other/Old Tales (1990)", RootFolder: "/other"},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("volumes = %+v, want %+v", got, want)
	}
}

func TestKapowarrAnswerWithAnErrorFailsTheCall(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://kapowarr.local/api/system/about?api_key=k1": {200, `{"error":"APIKeyInvalid","result":{}}`},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://kapowarr.local", "k1").Kapowarr()
	if _, err := client.SystemStatus(t.Context()); err == nil {
		t.Error("an error answer passed")
	}
}

func TestMylarReadsBothAnswerShapesOfGetIndex(t *testing.T) {
	row := `{"ComicID":"18001","ComicName":"Night Riders","ComicYear":"2019","Status":"Active","ComicLocation":"/comics/Night Riders (2019)","Have":"3"},` +
		`{"ComicID":"4050-18002","ComicName":"Old Tales","ComicYear":"1990","Status":"Paused","ComicLocation":"/other/Old Tales (1990)","Have":0}`
	for name, body := range map[string]string{"list": "[" + row + "]", "wrapped": `{"success":true,"data":[` + row + `]}`} {
		transport := &fakeTransport{responses: map[string]fakeResponse{"http://mylar.local/api?apikey=k2&cmd=getIndex": {200, body}}}
		client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://mylar.local", "k2").Mylar()
		got, err := client.Comics(t.Context())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := []arrimport.ComicSeries{
			{ID: "18001", Title: "Night Riders", Year: 2019, ComicVine: "18001", Monitored: true, HasFiles: true, Folder: "/comics/Night Riders (2019)", RootFolder: "/comics"},
			{ID: "4050-18002", Title: "Old Tales", Year: 1990, ComicVine: "18002", Folder: "/other/Old Tales (1990)", RootFolder: "/other"},
		}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("%s: comics = %+v, want %+v", name, got, want)
		}
	}
}

func TestMylarErrorAnswerFailsTheCall(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://mylar.local/api?apikey=bad&cmd=getIndex": {200, `{"success":false,"error":{"code":"401","message":"Incorrect API key"}}`},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://mylar.local", "bad").Mylar()
	if _, err := client.Comics(t.Context()); err == nil {
		t.Error("an error answer passed")
	}
}
