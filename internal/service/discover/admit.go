// Package discover decides what Discover recommends and what it may add by itself; it names no provider and no media type.
package discover

import (
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/media"
)

// Reason is why a recommendation was not admitted, a code the interface words.
type Reason string

// The reasons a recommendation is kept out of a library's list, and the extra ones auto-add adds.
const (
	ReasonHeldMonitored    Reason = "held_monitored"
	ReasonExcluded         Reason = "excluded"
	ReasonAboveRating      Reason = "above_rating"
	ReasonGenreExcluded    Reason = "genre_excluded"
	ReasonNetworkExcluded  Reason = "network_excluded"
	ReasonLanguageUnwanted Reason = "language_unwanted"
	ReasonOtherLibrary     Reason = "other_library"

	ReasonBelowRating      Reason = "below_rating"
	ReasonBelowVotes       Reason = "below_votes"
	ReasonAlreadyAutoAdded Reason = "already_auto_added"
	ReasonDailyCap         Reason = "daily_cap"
)

// Rejection is one reason with the value that caused it (the genre, the network, the language, the rating text).
type Rejection struct {
	Reason Reason
	Value  string
}

// Decision is whether a recommendation is admitted and every reason it is not.
type Decision struct {
	Admitted   bool
	Rejections []Rejection
}

// RatingLimit is a library's maximum age rating as the type reads certification texts.
type RatingLimit struct {
	// MaxAge is the highest age admitted, -1 for no limit.
	MaxAge       int64
	AllowUnrated bool
	// AgeOf reads a certification text; known is false for an empty or unreadable one.
	AgeOf func(certification string) (age int64, known bool)
}

// Rules are what a library keeps out of its list: its own exclusions joined with the instance's, a language list and the rating limit.
type Rules struct {
	ExcludeGenres   []string
	ExcludeNetworks []string
	// OriginalLanguages is empty for any language.
	OriginalLanguages []string
	Rating            RatingLimit
}

// Facts are what the library already knows of the item, looked up by the caller.
type Facts struct {
	// Monitored is the library holding it and wanting it.
	Monitored bool
	// Excluded is the item being in the exclusion list.
	Excluded bool
	// OtherLibrary is the suggestion rules naming another library of this type for it.
	OtherLibrary bool
}

// Union joins exclusion lists, a value once however it is spelled.
func Union(lists ...[]string) []string {
	var out []string
	for _, list := range lists {
		for _, value := range list {
			if value = strings.TrimSpace(value); value != "" && !containsFold(out, value) {
				out = append(out, value)
			}
		}
	}
	return out
}

func containsFold(list []string, value string) bool {
	return slices.ContainsFunc(list, func(one string) bool { return strings.EqualFold(strings.TrimSpace(one), strings.TrimSpace(value)) })
}

// Admit decides whether an item belongs in a library's list; every rule is checked so a preview shows each reason.
func Admit(item media.Recommendation, rules Rules, facts Facts) Decision {
	var rejections []Rejection
	if facts.Monitored {
		rejections = append(rejections, Rejection{Reason: ReasonHeldMonitored})
	}
	if facts.Excluded {
		rejections = append(rejections, Rejection{Reason: ReasonExcluded})
	}
	if rejected, value := aboveRating(item.Traits.Rating, rules.Rating); rejected {
		rejections = append(rejections, Rejection{Reason: ReasonAboveRating, Value: value})
	}
	for _, genre := range item.Traits.Genres {
		if containsFold(rules.ExcludeGenres, genre) {
			rejections = append(rejections, Rejection{Reason: ReasonGenreExcluded, Value: genre})
		}
	}
	for _, network := range item.Networks {
		if containsFold(rules.ExcludeNetworks, network) {
			rejections = append(rejections, Rejection{Reason: ReasonNetworkExcluded, Value: network})
		}
	}
	if len(rules.OriginalLanguages) > 0 && item.Traits.OriginalLanguage != "" && !containsFold(rules.OriginalLanguages, item.Traits.OriginalLanguage) {
		rejections = append(rejections, Rejection{Reason: ReasonLanguageUnwanted, Value: item.Traits.OriginalLanguage})
	}
	if facts.OtherLibrary {
		rejections = append(rejections, Rejection{Reason: ReasonOtherLibrary})
	}
	return Decision{Admitted: len(rejections) == 0, Rejections: rejections}
}

func aboveRating(certification string, limit RatingLimit) (rejected bool, value string) {
	if limit.MaxAge < 0 || limit.AgeOf == nil {
		return false, ""
	}
	age, known := limit.AgeOf(certification)
	if !known {
		return !limit.AllowUnrated, certification
	}
	return age > limit.MaxAge, certification
}

// AutoAddGates are a library's conditions for adding a recommendation without being asked.
type AutoAddGates struct {
	MinRating float64
	MinVotes  int64
	// PerDay is the most one library adds a day, 0 for no ceiling.
	PerDay int64
}

// AutoAddFacts are what the record says of the library and the item.
type AutoAddFacts struct {
	// HasRating is false when the feed's source carries no ratings, which skips the rating gate.
	HasRating bool
	// AddedBefore is auto-add having added the item to this library once already.
	AddedBefore bool
	// AddedToday is how many items auto-add added to the library in the last day.
	AddedToday int64
}

// AutoAdmit decides whether an admitted item may also be added by itself.
func AutoAdmit(item media.Recommendation, gates AutoAddGates, facts AutoAddFacts) Decision {
	var rejections []Rejection
	if facts.HasRating && item.Rating < gates.MinRating {
		rejections = append(rejections, Rejection{Reason: ReasonBelowRating, Value: strconv.FormatFloat(item.Rating, 'f', 1, 64)})
	}
	if item.Votes < gates.MinVotes {
		rejections = append(rejections, Rejection{Reason: ReasonBelowVotes, Value: strconv.FormatInt(item.Votes, 10)})
	}
	if facts.AddedBefore {
		rejections = append(rejections, Rejection{Reason: ReasonAlreadyAutoAdded})
	}
	if gates.PerDay > 0 && facts.AddedToday >= gates.PerDay {
		rejections = append(rejections, Rejection{Reason: ReasonDailyCap, Value: strconv.FormatInt(gates.PerDay, 10)})
	}
	return Decision{Admitted: len(rejections) == 0, Rejections: rejections}
}
