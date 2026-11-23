package providerdef

import (
	"io"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func memStore() Store {
	return Store{FS: fsx.NewMem(clock.NewFake(time.Unix(1_700_000_000, 0))), Root: "/cfg/providers"}
}

func put(t *testing.T, s Store, file, body string) {
	t.Helper()
	if err := s.FS.MkdirAll(file[:len(file)-len(baseName(file))-1], 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := s.FS.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, body)
	_ = w.Close()
}

func baseName(file string) string {
	for i := len(file) - 1; i >= 0; i-- {
		if file[i] == '/' {
			return file[i+1:]
		}
	}
	return file
}

func descriptor(key string) string {
	return "schema: 1\nkind: metadata\nkey: " + key + "\nname: N\nengine: builtin\n"
}

func TestPrepareWritesTheMirrorLeavesEqualFilesAndRemovesStaleOnesButNeverTouchesCustom(t *testing.T) {
	s := memStore()
	files := map[Kind]map[string][]byte{KindMetadata: {"alpha": []byte(descriptor("alpha"))}}
	if err := s.Prepare(t.Context(), []byte("readme"), files); err != nil {
		t.Fatal(err)
	}
	put(t, s, s.Root+"/metadata/builtin/stale.yaml", descriptor("stale"))
	put(t, s, s.Root+"/metadata/custom/mine.yaml", descriptor("mine"))
	before, _ := s.FS.Stat(s.Root + "/metadata/builtin/alpha.yaml")
	if err := s.Prepare(t.Context(), []byte("readme"), files); err != nil {
		t.Fatal(err)
	}
	after, _ := s.FS.Stat(s.Root + "/metadata/builtin/alpha.yaml")
	if !after.ModTime.Equal(before.ModTime) || after.Inode != before.Inode {
		t.Errorf("an equal file was rewritten: %+v then %+v", before, after)
	}
	if _, err := s.FS.Stat(s.Root + "/metadata/builtin/stale.yaml"); err == nil {
		t.Error("a file no longer shipped stayed")
	}
	if _, err := s.FS.Stat(s.Root + "/metadata/custom/mine.yaml"); err != nil {
		t.Errorf("custom file touched: %v", err)
	}
}

func TestLoadCustomKeepsGoodFilesAndReportsBadOnes(t *testing.T) {
	s := memStore()
	if err := s.Prepare(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
	put(t, s, s.Root+"/metadata/custom/good.yaml", descriptor("good"))
	put(t, s, s.Root+"/metadata/custom/bad.yaml", descriptor("good"))
	put(t, s, s.Root+"/metadata/custom/.removed/old.yaml", descriptor("old"))
	defs, reports, err := s.LoadCustom(KindMetadata)
	if err != nil || len(defs) != 1 || defs[0].Key != "good" || len(reports) != 1 || reports[0].File != "bad.yaml" || reports[0].Problems[0].Code != CodeKeyMismatch {
		t.Errorf("defs %+v reports %+v err %v", defs, reports, err)
	}
}

func TestCustomOverridesBuiltinAndApprovalFollowsHosts(t *testing.T) {
	builtin, _ := Parse(KindMetadata, "alpha.yaml", []byte(descriptor("alpha")), SourceBuiltin)
	custom, _ := Parse(KindMetadata, "alpha.yaml", []byte(descriptor("alpha")+"hosts: [Api.Example.test]\n"), SourceCustom)
	catalog := NewCatalog([]Definition{builtin}, []Definition{custom})
	got, _ := catalog.Get(KindMetadata, "alpha")
	if got.Source != SourceCustom || !got.Overrides {
		t.Errorf("got %+v", got)
	}
	http := custom
	http.Engine = EngineHTTP
	if StatusOf(http, State{}) != StatusNeedsApproval {
		t.Error("an unapproved http file is active")
	}
	approved := State{ApprovedHash: "h", ApprovedHosts: []string{"api.example.test"}}
	http.Hash = "changed"
	if StatusOf(http, approved) != StatusActive {
		t.Error("a changed body with the same hosts lost its approval")
	}
	http.Hosts = []string{"api.example.test", "other.example.test"}
	if StatusOf(http, approved) != StatusNeedsApproval {
		t.Error("a changed host list kept its approval")
	}
	off := false
	if StatusOf(builtin, State{Enabled: &off}) != StatusOff {
		t.Error("a person's off was ignored")
	}
}

func TestRemoveCustomMovesTheFileAndKeepsTheNewestFiftyRemoved(t *testing.T) {
	s := memStore()
	for unix := int64(1); unix <= MaxRemovedFiles+3; unix++ {
		if err := s.WriteCustom(KindMetadata, "alpha", []byte(descriptor("alpha"))); err != nil {
			t.Fatal(err)
		}
		if err := s.RemoveCustom(KindMetadata, "alpha", unix); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ReadCustom(KindMetadata, "alpha"); err == nil {
		t.Fatal("the removed file is still in custom/")
	}
	entries, err := s.FS.ReadDir("/cfg/providers/metadata/custom/.removed")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != MaxRemovedFiles {
		t.Fatalf("%d removed files kept, want %d", len(entries), MaxRemovedFiles)
	}
	for _, oldest := range []string{"alpha.1.yaml", "alpha.2.yaml", "alpha.3.yaml"} {
		if _, err := s.FS.Stat("/cfg/providers/metadata/custom/.removed/" + oldest); err == nil {
			t.Fatalf("%s survived the prune", oldest)
		}
	}
}

func TestWriteCustomRefusesAKeyThatIsNotAFileName(t *testing.T) {
	if err := memStore().WriteCustom(KindMetadata, "../escape", []byte("x")); err == nil {
		t.Fatal("a path in the key was accepted")
	}
}
