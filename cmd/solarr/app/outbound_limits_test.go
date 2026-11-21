package app

import "testing"

func TestEveryConnectionFamilyHasAConcurrencyCap(t *testing.T) {
	for _, prefix := range []string{providerIndexerPrefix, providerDownloaderPrefix, providerArrPrefix} {
		if providerFamilyLimits[prefix].MaxConcurrent != connectionConcurrency {
			t.Errorf("%s MaxConcurrent = %d, want %d", prefix, providerFamilyLimits[prefix].MaxConcurrent, connectionConcurrency)
		}
	}
}
