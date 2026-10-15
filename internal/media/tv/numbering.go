package tv

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
)

// OrderingSource is TheTVDB's own API: which orderings a series has, and its episodes in each.
type OrderingSource interface {
	Series(ctx context.Context, id int64) (tvdb.Series, error)
	Episodes(ctx context.Context, id int64, order string) ([]tvdb.Episode, error)
}

// FieldNumberingBasis is the work field that says which list a series' episodes are numbered by.
const FieldNumberingBasis = "numbering_basis"

// The numbering bases: TheTVDB's aired order is the default every provider shares; another of its orderings is
// "tvdb:<type>" (dvd, absolute, alternate, regional, altdvd).
const (
	basisPrefix  = ProviderTVDB + ":"
	basisAired   = basisPrefix + "aired"
	officialType = "official"
)

// ErrUnknownOrdering is a basis the series does not have.
var ErrUnknownOrdering = fmt.Errorf("tv: %w", media.ErrUnknownNumbering)

// ErrNoOrderings means TheTVDB is not wired, or the series has no TheTVDB id.
var ErrNoOrderings = fmt.Errorf("tv: the series' orderings are not known: %w", media.ErrNoNumbering)

// NumberingChoices answers the orderings TheTVDB has for the series, aired first, and the one it is numbered by.
func (s Service) NumberingChoices(ctx context.Context, workID int64) ([]media.Choice, string, error) {
	id, err := s.tvdbID(ctx, workID)
	if err != nil {
		return nil, "", err
	}
	series, err := s.Orderings.Series(ctx, id)
	if err != nil {
		return nil, "", err
	}
	choices := []media.Choice{{Key: basisAired, Label: "Aired (TheTVDB)"}}
	for _, kind := range series.SeasonTypes {
		if kind.Type == officialType || kind.Type == "" {
			continue
		}
		choices = append(choices, media.Choice{Key: basisPrefix + kind.Type, Label: kind.Name + " (TheTVDB)"})
	}
	manual, err := s.Reader.ManualFields(ctx, workID)
	if err != nil {
		return nil, "", err
	}
	current := manual[FieldNumberingBasis]
	if !strings.HasPrefix(current, basisPrefix) || current == basisPrefix+officialType {
		current = basisAired
	}
	return choices, current, nil
}

// PlanNumbering answers what numbering the series by basis would change, changing nothing.
func (s Service) PlanNumbering(ctx context.Context, workID int64, basis string) (media.NumberingPlan, error) {
	plan, _, err := s.planNumbering(ctx, workID, basis)
	return plan, err
}

// ApplyNumbering numbers the series by basis from now on: its episodes move to their places in that list, keeping
// their files, history and ids, and the choice is kept for every later refresh. Its files keep their names until
// they are renamed.
func (s Service) ApplyNumbering(ctx context.Context, request RefreshRequest, basis string) (media.NumberingPlan, error) {
	plan, moves, err := s.planNumbering(ctx, request.WorkID, basis)
	if err != nil {
		return media.NumberingPlan{}, err
	}
	if err := s.Series.Renumber(ctx, request.WorkID, moves); err != nil {
		return media.NumberingPlan{}, err
	}
	value := basis
	if basis == basisAired {
		value = ""
	}
	if err := s.Series.SetManualField(ctx, s.Clock.Now(), request.WorkID, FieldNumberingBasis, value); err != nil {
		return media.NumberingPlan{}, err
	}
	if _, err := s.Refresh(ctx, request); err != nil {
		return plan, fmt.Errorf("tv: renumbered, but the refresh after it failed: %w", err)
	}
	return plan, nil
}

// planNumbering checks the basis is one the series has and plans its moves.
func (s Service) planNumbering(ctx context.Context, workID int64, basis string) (media.NumberingPlan, []library.PartMove, error) {
	choices, _, err := s.NumberingChoices(ctx, workID)
	if err != nil {
		return media.NumberingPlan{}, nil, err
	}
	known := false
	for _, choice := range choices {
		known = known || choice.Key == basis
	}
	if !known {
		return media.NumberingPlan{}, nil, fmt.Errorf("%w: %q", ErrUnknownOrdering, basis)
	}
	id, err := s.tvdbID(ctx, workID)
	if err != nil {
		return media.NumberingPlan{}, nil, err
	}
	episodes, err := s.Orderings.Episodes(ctx, id, orderOf(basis))
	if err != nil {
		return media.NumberingPlan{}, nil, err
	}
	return s.movesFor(ctx, workID, episodes)
}

// movesFor compares the series' parts with an ordering's list by TheTVDB episode id, which every ordering shares.
func (s Service) movesFor(ctx context.Context, workID int64, episodes []tvdb.Episode) (media.NumberingPlan, []library.PartMove, error) {
	parts, err := s.Series.PartIDs(ctx, workID, ProviderTVDB+":episode")
	if err != nil {
		return media.NumberingPlan{}, nil, err
	}
	placeOf := map[string]Key{}
	taken := map[string]bool{}
	for _, episode := range episodes {
		ref := MediaRef(episode.SeasonNumber, episode.Number)
		placeOf[strconv.FormatInt(episode.ID, 10)] = Key{episode.SeasonNumber, episode.Number}
		taken[ref] = true
	}
	var plan media.NumberingPlan
	var moves []library.PartMove
	held := map[string]bool{}
	for _, part := range parts {
		place, listed := placeOf[part.ExternalID]
		switch {
		case listed && MediaRef(place.Season, place.Number) == part.MediaRef:
			plan.Unchanged++
			held[part.MediaRef] = true
		case listed:
			to := MediaRef(place.Season, place.Number)
			moves = append(moves, library.PartMove{PartID: part.PartID, OldRef: part.MediaRef, NewRef: to, NewSortKey: sortKey(place.Season, place.Number)})
			plan.Moves = append(plan.Moves, media.NumberingMove{Title: part.Title, From: refLabel(part.MediaRef), To: refLabel(to), HasFile: part.HasFile})
			held[to] = true
		case taken[part.MediaRef]:
			// Its place belongs to another episode in this list, and it has none of its own there.
			aside := library.UnplacedRef + strconv.FormatInt(part.PartID, 10)
			moves = append(moves, library.PartMove{PartID: part.PartID, OldRef: part.MediaRef, NewRef: aside, NewSortKey: "~" + aside})
			plan.SetAside = append(plan.SetAside, media.NumberingMove{Title: part.Title, From: refLabel(part.MediaRef), HasFile: part.HasFile})
		}
	}
	for _, episode := range episodes {
		if !held[MediaRef(episode.SeasonNumber, episode.Number)] {
			plan.New++
		}
	}
	return plan, moves, nil
}

// keepOrdering numbers a refreshed series by the ordering a person chose, and moves its parts when TheTVDB's list
// itself has shifted since; the aired order needs neither.
func (s Service) keepOrdering(ctx context.Context, workID int64, series *Series) error {
	basis, err := s.numberedByOrdering(ctx, workID)
	if err != nil || basis == "" {
		return err
	}
	episodes, err := s.Orderings.Episodes(ctx, series.IDs.TVDB, orderOf(basis))
	if err != nil {
		return err
	}
	applyOrdering(series, episodes, basis)
	_, moves, err := s.movesFor(ctx, workID, episodes)
	if err != nil || len(moves) == 0 {
		return err
	}
	return s.Series.Renumber(ctx, workID, moves)
}

// applyOrdering numbers a fetched series by one of TheTVDB's orderings: each episode keeps what the providers said of
// it and takes its place in that list; one the list lacks is left out, one only the list has comes from it.
func applyOrdering(series *Series, episodes []tvdb.Episode, basis string) {
	byID := map[int64]Episode{}
	for _, episode := range series.Episodes {
		if id := episode.IDs[ProviderTVDB]; id != 0 {
			byID[id] = episode
		}
	}
	ordered := make([]Episode, 0, len(episodes))
	for _, listed := range episodes {
		episode, found := byID[listed.ID]
		if !found {
			episode = Episode{
				Title: listed.Name, Overview: listed.Overview, AirDate: listed.Aired, RuntimeMin: listed.Runtime,
				Image: listed.Image, IDs: map[string]int64{ProviderTVDB: listed.ID}, AirConfidence: ConfidenceUnknown,
			}
			if listed.Aired != "" {
				episode.AirConfidence, episode.AirBasis = ConfidenceLow, "tvdb air date"
			}
		}
		episode.Season, episode.Number, episode.Special = listed.SeasonNumber, listed.Number, listed.SeasonNumber == 0
		if listed.AbsoluteNumber > 0 {
			episode.Absolute = listed.AbsoluteNumber
		}
		ordered = append(ordered, episode)
	}
	series.Episodes, series.Basis = ordered, basis
}

// numberedByOrdering answers the TheTVDB ordering a series is numbered by, "" for the aired one every provider shares.
func (s Service) numberedByOrdering(ctx context.Context, workID int64) (string, error) {
	if s.Orderings == nil {
		return "", nil
	}
	manual, err := s.Reader.ManualFields(ctx, workID)
	if err != nil {
		return "", err
	}
	basis := manual[FieldNumberingBasis]
	if !strings.HasPrefix(basis, basisPrefix) || basis == basisAired || basis == basisPrefix+officialType {
		return "", nil
	}
	return basis, nil
}

func (s Service) tvdbID(ctx context.Context, workID int64) (int64, error) {
	if s.Orderings == nil {
		return 0, ErrNoOrderings
	}
	ids, err := s.Reader.ProviderIDs(ctx, workID)
	if err != nil {
		return 0, err
	}
	id, valid := ParseID(ids[ProviderTVDB])
	if !valid {
		return 0, ErrNoOrderings
	}
	return id, nil
}

func orderOf(basis string) string {
	order := strings.TrimPrefix(basis, basisPrefix)
	if order == "aired" {
		return officialType
	}
	return order
}

// refLabel is a place as a person reads it: "S01E05".
func refLabel(ref string) string {
	season, episode := splitRef(ref)
	return fmt.Sprintf("S%02dE%02d", season, episode)
}
