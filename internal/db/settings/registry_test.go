package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestRegistry_RegisterRefusesADuplicateKey(t *testing.T) {
	registry := settings.NewRegistry()
	def := settings.SettingDef{Key: "x.y", Kind: settings.KindBool, Tier: settings.TierNormal}
	if err := registry.Register(def); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := registry.Register(def); err == nil {
		t.Fatal("a duplicate key was accepted")
	}
}

func TestRegistry_RegisterRefusesAnInvalidTier(t *testing.T) {
	registry := settings.NewRegistry()
	err := registry.Register(settings.SettingDef{Key: "x.y", Kind: settings.KindBool, Tier: "not-a-tier"})
	if err == nil {
		t.Fatal("an invalid tier was accepted")
	}
}

func TestRegistry_RegisterRefusesAMissingKind(t *testing.T) {
	registry := settings.NewRegistry()
	err := registry.Register(settings.SettingDef{Key: "x.y", Tier: settings.TierNormal})
	if err == nil {
		t.Fatal("a definition with no kind was accepted")
	}
}

func TestRegistry_MustRegisterPanicsOnDuplicate(t *testing.T) {
	registry := settings.NewRegistry()
	def := settings.SettingDef{Key: "x.y", Kind: settings.KindBool, Tier: settings.TierNormal}
	registry.MustRegister(def)

	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister did not panic on a duplicate key")
		}
	}()
	registry.MustRegister(def)
}

func TestRegistry_Lookup(t *testing.T) {
	registry := settings.NewRegistry()
	registry.MustRegister(settings.SettingDef{Key: "x.y", Kind: settings.KindBool, Tier: settings.TierNormal})

	if _, found := registry.Lookup("x.y"); !found {
		t.Error("Lookup did not find a registered key")
	}
	if _, found := registry.Lookup("nope"); found {
		t.Error("Lookup found a key that was never registered")
	}
}

func TestRegistry_LookupAll(t *testing.T) {
	registry := settings.NewRegistry()
	registry.MustRegister(settings.SettingDef{Key: "a", Kind: settings.KindBool, Tier: settings.TierNormal})
	registry.MustRegister(settings.SettingDef{Key: "b", Kind: settings.KindBool, Tier: settings.TierNormal})

	if err := registry.LookupAll("a", "b"); err != nil {
		t.Errorf("LookupAll with every key registered: %v", err)
	}
	err := registry.LookupAll("a", "c")
	if err == nil {
		t.Fatal("LookupAll accepted a key that was never registered")
	}
	if !errors.Is(err, settings.ErrSettingNotRegistered) {
		t.Errorf("LookupAll error = %v, want it to wrap ErrSettingNotRegistered", err)
	}
}

func TestRegistry_AllIsSortedByKey(t *testing.T) {
	registry := settings.NewRegistry()
	registry.MustRegister(settings.SettingDef{Key: "z", Kind: settings.KindBool, Tier: settings.TierNormal})
	registry.MustRegister(settings.SettingDef{Key: "a", Kind: settings.KindBool, Tier: settings.TierNormal})
	registry.MustRegister(settings.SettingDef{Key: "m", Kind: settings.KindBool, Tier: settings.TierNormal})

	all := registry.All()
	if len(all) != 3 || all[0].Key != "a" || all[1].Key != "m" || all[2].Key != "z" {
		t.Errorf("All() = %v, want sorted a, m, z", all)
	}
}

// The mutation this guards against: a write path forgets to register a new
// key and nothing ever notices (ADR-26: "a setting not in the registry
// cannot be written"). This pins down every scalar setting the codebase
// currently writes through Cache's shared instance_setting chokepoint, so
// deleting one from NewDefaultRegistry breaks this test rather than
// silently losing its tier.
func TestNewDefaultRegistry_CoversEveryKnownScalarSetting(t *testing.T) {
	registry := settings.NewDefaultRegistry()

	known := []string{
		settings.KeyAuthRequired,
		settings.KeyUsersNewLibraryPermission,
		settings.KeyBackupRetention, settings.KeyBackupKeepCount, settings.KeyBackupKeepDays,
		settings.KeyImportAutoCommitConfidence, settings.KeyImportTransferLadder,
		settings.KeyImportRenameOnImport,
		settings.KeyImportExtraFiles, settings.KeyImportExtraFileExtensions,
		settings.KeyNetworkPort, settings.KeyNetworkProxyEnabled, settings.KeyNetworkProxyURL,
		settings.KeyNetworkProxyBypass, settings.KeyNetworkAllowCIDRsEnabled, settings.KeyNetworkAllowCIDRs,
		settings.KeyCacheMaxBytes, settings.KeyCacheMaxAgeDays, settings.KeyCacheImagesShare, settings.KeyCacheMetadataShare,
		settings.KeyPriorityUserRequestWeight, settings.KeyPriorityNewReleaseWeight,
		settings.KeyPriorityGapWeight, settings.KeyPriorityUpgradeWeight, settings.KeyPriorityNewReleaseDays,
		settings.KeyBrainzMashUserAgent, settings.KeyMetadataSourceOrder,
		settings.KeyMetadataCoverSources, settings.KeyMetadataWikidata, settings.KeyMetadataAcoustID,
		settings.KeyAcoustIDAPIKey,
		// The non-scalar audit's own KindEntity additions (2026-09-12): each
		// names a whole settings surface, never an instance_setting row, but
		// belongs in this same "nothing is registered by accident or left
		// out by accident" guard.
		settings.EntityRootFolder, settings.EntityCategory, settings.EntityQualityProfile,
		settings.EntityLanguageProfile, settings.EntityExclusionPattern, settings.EntityArrInstance,
		settings.EntityImportList, settings.EntityDownloadFolder,
		// Stage 5's own entities (2026-09-12): indexer and download client connections.
		settings.EntityIndexer, settings.EntityDownloader,
	}
	for _, key := range known {
		if _, found := registry.Lookup(key); !found {
			t.Errorf("NewDefaultRegistry: %q is not registered", key)
		}
	}
	if got := len(registry.All()); got != len(known) {
		t.Errorf("NewDefaultRegistry has %d settings, want exactly %d -- a key was added or removed "+
			"without updating this test", got, len(known))
	}
}

func TestNewDefaultRegistry_EveryDefaultParsesAsItsOwnKind(t *testing.T) {
	registry := settings.NewDefaultRegistry()
	for _, def := range registry.All() {
		if def.Name == "" {
			t.Errorf("%s: no name", def.Key)
		}
		if def.HelpText == "" {
			t.Errorf("%s: no help text", def.Key)
		}
	}
}
