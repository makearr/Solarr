package apiserver

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// RACE-5: overlapping patches of config.yml all land; none starts from bytes another has already replaced.
func TestPatchConfig_OverlappingPatchesAllLand(t *testing.T) {
	mem := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := mem.MkdirAll("/config", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteAtomicSecret(mem, "/config/config.yml", func(w io.Writer) error {
		_, err := w.Write([]byte("start\n"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{fsys: mem, configPath: "/config/config.yml"}
	var group sync.WaitGroup
	for index := range 24 {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := server.patchConfig(func(data []byte) ([]byte, error) {
				return append(data, fmt.Sprintf("key%d\n", index)...), nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	data, err := server.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	for index := range 24 {
		if !strings.Contains(string(data), fmt.Sprintf("key%d\n", index)) {
			t.Errorf("patch %d was lost:\n%s", index, data)
		}
	}
}
