package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// EgressStatus is what the public-address check last learned. Mirrored here so
// apiserver does not depend on the package that performs the check.
type EgressStatus struct {
	Enabled   bool
	PublicIP  string
	Source    string
	CheckedAt time.Time
	Err       string
}

// EgressReporter supplies that status, checking first when the last answer is stale: asking is what turns the
// check on (the status bar's address element). Optional: without one the endpoint reports a disabled check.
type EgressReporter interface {
	Status(ctx context.Context) EgressStatus
}

// GetEgress reports the address this host appears to connect from. Authenticated:
// it is a fact about the operator's network, not build identity.
func (s *Server) GetEgress(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}

	if s.egress == nil {
		writeJSON(w, s.logger, http.StatusOK, gen.Egress{Enabled: boolPtr(false)})
		return
	}

	status := s.egress.Status(r.Context())
	view := gen.Egress{Enabled: &status.Enabled}
	if status.PublicIP != "" {
		view.PublicIp = &status.PublicIP
	}
	if status.Source != "" {
		view.Source = &status.Source
	}
	if !status.CheckedAt.IsZero() {
		checked := status.CheckedAt
		view.CheckedAt = &checked
	}
	if status.Err != "" {
		view.Error = &status.Err
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

func boolPtr(value bool) *bool { return &value }
