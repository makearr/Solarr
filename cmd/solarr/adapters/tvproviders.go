package adapters

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/api/apiserver"
)

// TVProviderProbe is one TV metadata provider: how it is listed, what key state it is in and one cheap question to ask it.
type TVProviderProbe struct {
	Info apiserver.TVProviderInfo
	// Keyed answers the key state live, since a key saved in the interface applies at once; nil keeps Info.Key.
	Keyed func() string
	Ping  func(ctx context.Context) error
}

// TVProviders is apiserver.TVProviders over the process's providers.
type TVProviders struct{ Probes []TVProviderProbe }

// List answers every provider with its current key state.
func (p TVProviders) List() []apiserver.TVProviderInfo {
	listed := make([]apiserver.TVProviderInfo, 0, len(p.Probes))
	for _, probe := range p.Probes {
		info := probe.Info
		if probe.Keyed != nil {
			info.Key = probe.Keyed()
		}
		listed = append(listed, info)
	}
	return listed
}

// Test asks each provider its one question; a provider that needs a key it lacks is reported, not asked.
func (p TVProviders) Test(ctx context.Context) []apiserver.TVProviderResult {
	results := make([]apiserver.TVProviderResult, 0, len(p.Probes))
	for _, probe := range p.Probes {
		result := apiserver.TVProviderResult{ID: probe.Info.ID}
		if probe.Keyed != nil && probe.Keyed() == "missing" {
			result.Message = "No key stored: add yours to use this provider."
			results = append(results, result)
			continue
		}
		if err := probe.Ping(ctx); err != nil {
			result.Message = describeProbeError(err)
		} else {
			result.OK, result.Message = true, "Answered."
		}
		results = append(results, result)
	}
	return results
}

func describeProbeError(err error) string { return fmt.Sprintf("Failed: %v", err) }
