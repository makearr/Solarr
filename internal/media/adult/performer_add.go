package adult

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
)

// KindPerformer is the add kind that looks up and follows a performer, Whisparr's "Add performer".
const KindPerformer = "performer"

// PerformerAdds is the part of the add dialog that follows a performer: it makes the person with what the server says of
// them and follows them back to their first scene, the way adding an artist does.
type PerformerAdds struct {
	People interface {
		FindByExternalID(ctx context.Context, provider, externalID string) (int64, bool, error)
		ResolvePerson(ctx context.Context, at time.Time, link library.PersonLink) (int64, error)
		BackfillDetails(ctx context.Context, personID int64, identity library.PersonIdentity) error
		SetExtras(ctx context.Context, personID int64, extras library.PersonExtras) error
		Subscribe(ctx context.Context, at time.Time, personID, userID int64, request library.SubscriptionRequest) (int64, error)
	}
	Now func() time.Time
}

// Lookup finds performers by name.
func (a PerformerAdds) Lookup(ctx context.Context, service Service, lookup media.AddLookup) ([]media.AddCandidate, error) {
	if len([]rune(strings.TrimSpace(lookup.Query))) < 2 {
		return nil, media.ErrQueryTooShort
	}
	hits, err := service.SearchPerformers(ctx, lookup.CategoryID, lookup.Query, lookup.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	candidates := make([]media.AddCandidate, 0, len(hits))
	for _, hit := range hits {
		performer := hit.Performer
		candidate := media.AddCandidate{
			Kind: KindPerformer, Provider: performerProvider(hit.Server), ExternalID: performer.ID, Title: performer.Name,
			Subtitle: performerSubtitle(performer),
		}
		if len(performer.Images) > 0 {
			candidate.PosterURL = performer.Images[0].URL
		}
		if len(performer.BirthDate) >= 4 {
			year, _ := strconv.Atoi(performer.BirthDate[:4])
			candidate.Year = int64(year)
		}
		if id, held, err := a.People.FindByExternalID(ctx, candidate.Provider, candidate.ExternalID); err == nil && held {
			candidate.LibraryID = id
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// performerSubtitle tells two performers of one name apart: what the server calls them apart by, their country, how many
// scenes it holds of them.
func performerSubtitle(performer stashbox.Performer) string {
	var parts []string
	if performer.Disambiguation != "" {
		parts = append(parts, performer.Disambiguation)
	}
	if performer.Country != "" {
		parts = append(parts, performer.Country)
	}
	if performer.SceneCount > 0 {
		parts = append(parts, strconv.Itoa(performer.SceneCount)+" scenes")
	}
	return strings.Join(parts, " - ")
}

// Add makes the performer a person of the library with their details and, when monitored, follows them: their scenes are
// added as the follow runs.
func (a PerformerAdds) Add(ctx context.Context, service Service, categoryID int64, request media.AddRequest) (media.AddResult, error) {
	server, known := strings.CutSuffix(request.Provider, ":performer")
	if !known || service.Servers[server] == nil {
		return media.AddResult{}, fmt.Errorf("%w: %s is not a performer provider", media.ErrInvalidAdd, request.Provider)
	}
	// Read again rather than trusted from the request: an id names the performer, the server says the rest.
	performer, err := service.Servers[server].FindPerformer(ctx, request.ExternalID)
	if errors.Is(err, stashbox.ErrNotFound) {
		return media.AddResult{}, fmt.Errorf("%w: performer %s", media.ErrInvalidAdd, request.ExternalID)
	}
	if err != nil {
		return media.AddResult{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	identity, extras := PerformerDetails(performer)
	now := a.Now()
	personID, err := a.People.ResolvePerson(ctx, now, library.PersonLink{Provider: request.Provider, ExternalID: request.ExternalID, Person: identity})
	if err != nil {
		return media.AddResult{}, fmt.Errorf("adult: resolve performer: %w", err)
	}
	if err := a.People.BackfillDetails(ctx, personID, identity); err != nil {
		return media.AddResult{}, err
	}
	if err := a.People.SetExtras(ctx, personID, extras); err != nil {
		return media.AddResult{}, err
	}
	if request.Monitored {
		// Back to the first scene: a performer added on purpose is wanted whole.
		_, err := a.People.Subscribe(ctx, now, personID, request.UserID, library.SubscriptionRequest{
			MediaTypes: []string{MediaType}, Roles: []string{RolePerformer}, MonitorBackCatalogue: true, CategoryID: categoryID,
		})
		if err != nil {
			return media.AddResult{}, fmt.Errorf("adult: follow performer: %w", err)
		}
	}
	return media.AddResult{Kind: KindPerformer, PersonID: personID}, nil
}
