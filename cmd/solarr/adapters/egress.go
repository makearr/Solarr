package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/platform/egress"
)

// Egress adapts the prober to the interface apiserver defines, so neither
// package has to know about the other.
type Egress struct {
	Prober *egress.Prober
}

func (e Egress) Status(ctx context.Context) apiserver.EgressStatus {
	status := e.Prober.Demand(ctx)
	return apiserver.EgressStatus{
		Enabled:   status.Enabled,
		PublicIP:  status.PublicIP,
		Source:    status.Source,
		CheckedAt: status.CheckedAt,
		Err:       status.Err,
	}
}
