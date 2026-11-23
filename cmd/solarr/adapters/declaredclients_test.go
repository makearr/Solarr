package adapters_test

import (
	"os"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/specclient"
)

func TestADeclaredKindIsOfferedBesideTheCompiledOnesAndNeverReplacesOne(t *testing.T) {
	data, err := os.ReadFile("../../../internal/acquiring/specclient/testdata/sabnzbd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	port, err := specclient.Parse("sabnzbd-port", "SABnzbd (definition)", data)
	if err != nil {
		t.Fatal(err)
	}
	clash := port
	clash.Key = adapters.ImplSABnzbd
	adapters.SetDeclaredDownloadClients([]specclient.Definition{port, clash})
	t.Cleanup(func() { adapters.SetDeclaredDownloadClients(nil) })

	declared, known := adapters.DownloadClientDefinition("sabnzbd-port")
	if !known || declared.Protocol != download.ProtocolUsenet || declared.SecretLabel != "API key" || !declared.Categories {
		t.Errorf("declared kind = %+v, known %v", declared, known)
	}
	if compiled, _ := adapters.DownloadClientDefinition(adapters.ImplSABnzbd); compiled.Label != "SABnzbd" {
		t.Errorf("the compiled kind was replaced: %+v", compiled)
	}
	adapters.SetDeclaredDownloadClients(nil)
	if _, known := adapters.DownloadClientDefinition("sabnzbd-port"); known {
		t.Error("a removed definition is still offered")
	}
}
