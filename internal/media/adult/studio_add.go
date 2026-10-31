package adult

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
)

// KindStudio is the add kind that follows a studio, Whisparr's monitored studio. It has no lookup: a studio is added by
// the id a server or an *Arr import names it by.
const KindStudio = "studio"

// StudioAdds makes the studio an organisation of the library and follows it, so its new scenes are added as they come.
type StudioAdds struct {
	Organisations interface {
		Resolve(ctx context.Context, link library.OrganisationLink) (int64, bool, error)
	}
	Follows interface {
		Follow(ctx context.Context, at time.Time, subject library.Subject, userID int64, request library.FollowRequest) (library.Subscription, error)
	}
	Now func() time.Time
}

// Add resolves the studio at the server named by the provider ("stashdb:studio") and follows it in the library.
func (a StudioAdds) Add(ctx context.Context, service Service, categoryID int64, request media.AddRequest) (media.AddResult, error) {
	server, known := strings.CutSuffix(request.Provider, ":studio")
	if !known || service.Servers[server] == nil {
		return media.AddResult{}, fmt.Errorf("%w: %s is not a studio provider", media.ErrInvalidAdd, request.Provider)
	}
	studio, err := service.Servers[server].FindStudio(ctx, request.ExternalID)
	if errors.Is(err, stashbox.ErrNotFound) {
		return media.AddResult{}, fmt.Errorf("%w: studio %s", media.ErrInvalidAdd, request.ExternalID)
	}
	if err != nil {
		return media.AddResult{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	organisationID, created, err := a.Organisations.Resolve(ctx, library.OrganisationLink{Kind: "studio", Name: studio.Name, Provider: request.Provider, ExternalID: request.ExternalID})
	if err != nil {
		return media.AddResult{}, err
	}
	if request.Monitored {
		if _, err := a.Follows.Follow(ctx, a.Now(), library.Subject{Type: "organisation", ID: organisationID}, request.UserID,
			library.FollowRequest{CategoryID: categoryID, MediaType: MediaType}); err != nil {
			return media.AddResult{}, fmt.Errorf("adult: follow studio: %w", err)
		}
	}
	return media.AddResult{Kind: KindStudio, Created: created}, nil
}
