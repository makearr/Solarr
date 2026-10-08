package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/service/follow"
)

// Follow satisfies apiserver.Follower over the follow service, checking the caller may know of the subject first.
type Follow struct {
	Service       follow.Service
	People        library.People
	Organisations library.Organisations
}

// seen answers ErrPersonNotFound or ErrOrganisationNotFound for a subject no visible library knows of.
func (a Follow) seen(ctx context.Context, subjectType string, id int64, visible []int64) error {
	switch subjectType {
	case apiserver.SubjectPerson:
		if _, err := a.People.Get(ctx, id, visible); errors.Is(err, library.ErrPersonNotFound) {
			return fmt.Errorf("%w: %w", apiserver.ErrPersonNotFound, err)
		} else if err != nil {
			return err
		}
	case apiserver.SubjectOrganisation:
		if _, err := a.Organisations.Get(ctx, id, visible); errors.Is(err, library.ErrOrganisationNotFound) {
			return fmt.Errorf("%w: %w", apiserver.ErrOrganisationNotFound, err)
		} else if err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: %s cannot be followed", apiserver.ErrInvalidChoice, subjectType)
	}
	return nil
}

// Follows maps the user's follows of a subject in the libraries they may read.
func (a Follow) Follows(ctx context.Context, subjectType string, subjectID, userID int64, visible []int64) ([]gen.Follow, error) {
	if err := a.seen(ctx, subjectType, subjectID, visible); err != nil {
		return nil, err
	}
	follows, err := a.Service.Follows(ctx, library.Subject{Type: subjectType, ID: subjectID}, userID)
	if err != nil {
		return nil, err
	}
	views := []gen.Follow{}
	for _, followed := range follows {
		if slices.Contains(visible, followed.CategoryID) {
			views = append(views, followView(followed))
		}
	}
	return views, nil
}

// Follow follows the subject into the request's library.
func (a Follow) Follow(ctx context.Context, subjectType string, subjectID, userID int64, visible []int64, request gen.FollowRequest) (gen.Follow, error) {
	if err := a.seen(ctx, subjectType, subjectID, visible); err != nil {
		return gen.Follow{}, err
	}
	followed, err := a.Service.Follow(ctx, library.Subject{Type: subjectType, ID: subjectID}, userID, followRequest(request))
	if err != nil {
		return gen.Follow{}, translateFollowError(err)
	}
	return followView(followed), nil
}

// Unfollow ends the user's follow of the subject in one library.
func (a Follow) Unfollow(ctx context.Context, subjectType string, subjectID, userID, categoryID int64, visible []int64) error {
	if err := a.seen(ctx, subjectType, subjectID, visible); err != nil {
		return err
	}
	return a.Service.Unfollow(ctx, library.Subject{Type: subjectType, ID: subjectID}, userID, categoryID)
}

// Preview maps what following would add now.
func (a Follow) Preview(ctx context.Context, subjectType string, subjectID int64, visible []int64, request gen.FollowRequest) (gen.FollowPreview, error) {
	if err := a.seen(ctx, subjectType, subjectID, visible); err != nil {
		return gen.FollowPreview{}, err
	}
	preview, err := a.Service.Preview(ctx, library.Subject{Type: subjectType, ID: subjectID}, followRequest(request))
	if err != nil {
		return gen.FollowPreview{}, translateFollowError(err)
	}
	view := gen.FollowPreview{More: preview.More, Held: preview.Held, Limit: preview.Limit, WouldAdd: []gen.FollowCandidate{}}
	for _, candidate := range preview.WouldAdd {
		entry := gen.FollowCandidate{Provider: candidate.Provider, ExternalId: candidate.ExternalID, Title: candidate.Title}
		if candidate.Year > 0 {
			entry.Year = &candidate.Year
		}
		setString(&entry.PosterUrl, candidate.PosterURL)
		setString(&entry.Role, candidate.Role)
		view.WouldAdd = append(view.WouldAdd, entry)
	}
	return view, nil
}

func followRequest(request gen.FollowRequest) follow.Request {
	out := follow.Request{CategoryID: request.CategoryId}
	if request.Roles != nil {
		out.Roles = *request.Roles
	}
	if request.Options != nil {
		out.Options = *request.Options
	}
	if request.BackCatalogue != nil {
		out.BackCatalogue = *request.BackCatalogue
	}
	if request.Search != nil {
		out.Search = *request.Search
	}
	return out
}

func translateFollowError(err error) error {
	switch {
	case errors.Is(err, follow.ErrNotFollowable), errors.Is(err, follow.ErrNoLibrary):
		return fmt.Errorf("%w: %w", apiserver.ErrNotFollowable, err)
	case errors.Is(err, follow.ErrInvalidOption):
		return fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	case errors.Is(err, media.ErrNoFollowSource):
		return fmt.Errorf("%w: %w", apiserver.ErrFollowSourceMissing, err)
	}
	return err
}

// followView maps a stored follow, its options decoded from the filters: a single choice becomes a one-entry list.
func followView(followed library.Subscription) gen.Follow {
	view := gen.Follow{
		Id: followed.ID, CategoryId: followed.CategoryID, Roles: nonNil(followed.Roles), Options: map[string][]string{},
		BackCatalogue: followed.MonitorBackCatalogue, CreatedAt: followed.CreatedAt,
	}
	var stored map[string]json.RawMessage
	if followed.FiltersJSON != "" && json.Unmarshal([]byte(followed.FiltersJSON), &stored) == nil {
		for key, raw := range stored {
			var many []string
			var one string
			switch {
			case json.Unmarshal(raw, &many) == nil:
				view.Options[key] = many
			case json.Unmarshal(raw, &one) == nil:
				view.Options[key] = []string{one}
			}
		}
	}
	return view
}
