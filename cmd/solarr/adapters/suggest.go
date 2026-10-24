package adapters

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// Suggester answers which library a work belongs in by the libraries' rules (plan 10-libraries L5, ADR-56): a library
// that can hold its type and admits its rating, whose rule matching the most facts wins, its priority breaking ties.
type Suggester struct {
	Registry *media.Registry
	Settings *settings.Cache
	People   library.People
	Works    library.Works
}

// facts are what a suggestion reads of a work, from a lookup or from what the library holds.
type facts struct {
	genres    map[string]bool
	language  string
	countries []string
	aniList   bool
	// ratingAge is the youngest age its certification admits; ratingKnown false leaves the rating limits out.
	ratingAge   int64
	ratingKnown bool
}

// ForCandidate is the library a looked-up work of the media type belongs in; false when no library's rule says.
func (s Suggester) ForCandidate(ctx context.Context, mediaType string, traits media.Traits) (apiserver.LibrarySuggestion, bool, error) {
	descriptor, found := s.descriptor(mediaType)
	if !found {
		return apiserver.LibrarySuggestion{}, false, nil
	}
	genres, err := s.People.GenreTree(ctx, descriptor.Taxonomy.Key, traits.Genres)
	if err != nil {
		return apiserver.LibrarySuggestion{}, false, err
	}
	known := facts{genres: genres, language: traits.OriginalLanguage, countries: traits.OriginCountries, aniList: traits.AniList, ratingAge: -1}
	if rating, ok := media.Capability[media.Rating](s.Registry, media.Key(mediaType)); ok && traits.Rating != "" {
		known.ratingAge, known.ratingKnown = -1, true
		if age, read := rating.RatingAge(traits.Rating); read {
			known.ratingAge = age
		}
	}
	suggestion, score := s.best(s.Settings.Snapshot(), mediaType, known)
	return suggestion, score > 0, nil
}

// best is the library of the type the facts fit best, with how many facts its winning rule matched (0 for none).
func (s Suggester) best(snapshot *settings.Snapshot, mediaType string, known facts) (apiserver.LibrarySuggestion, int) {
	var winner settings.Category
	var winning []settings.RuleCondition
	for _, category := range snapshot.Categories() {
		if category.MediaType != mediaType || known.ratingKnown && !category.AdmitsAge(known.ratingAge) {
			continue
		}
		matched := bestRule(s.rulesOf(snapshot, category), known)
		if matched == nil {
			continue
		}
		library := snapshot.LibraryOf(category.ID)
		if len(matched) > len(winning) || len(matched) == len(winning) && s.rank(snapshot, library) < s.rank(snapshot, snapshot.LibraryOf(winner.ID)) {
			winner, winning = category, matched
		}
	}
	if winning == nil {
		return apiserver.LibrarySuggestion{}, 0
	}
	return apiserver.LibrarySuggestion{CategoryID: winner.ID, Name: winner.Name, Reason: reasonOf(winning)}, len(winning)
}

func (s Suggester) rank(snapshot *settings.Snapshot, categoryID int64) int64 {
	if category, found := snapshot.Category(categoryID); found {
		return category.PriorityRank
	}
	return 1 << 31
}

// rulesOf is a library's own rules, else its kind's shipped ones; a further media type follows its library.
func (s Suggester) rulesOf(snapshot *settings.Snapshot, category settings.Category) []settings.LibraryRule {
	head := category
	if first, found := snapshot.Category(snapshot.LibraryOf(category.ID)); found {
		head = first
	}
	if head.Rules != nil {
		return head.Rules
	}
	return s.KindRules(category.MediaType, cmp.Or(head.Kind, category.Kind))
}

// KindRules are the rules a media type ships for a kind of library.
func (s Suggester) KindRules(mediaType, kind string) []settings.LibraryRule {
	descriptor, found := s.descriptor(mediaType)
	if !found {
		return nil
	}
	for _, declared := range descriptor.Kinds {
		if declared.Key == kind {
			return declared.Rules
		}
	}
	return nil
}

func (s Suggester) descriptor(mediaType string) (media.Descriptor, bool) {
	for _, descriptor := range s.Registry.Descriptors() {
		if string(descriptor.Key) == mediaType {
			return descriptor, true
		}
	}
	return media.Descriptor{}, false
}

// bestRule is the matching rule with the most conditions, nil when none matches.
func bestRule(rules []settings.LibraryRule, known facts) []settings.RuleCondition {
	var best []settings.RuleCondition
	for _, rule := range rules {
		if len(rule.Conditions) > len(best) && matches(rule, known) {
			best = rule.Conditions
		}
	}
	return best
}

func matches(rule settings.LibraryRule, known facts) bool {
	for _, condition := range rule.Conditions {
		value := strings.ToLower(strings.TrimSpace(condition.Value))
		switch condition.Field {
		case settings.RuleGenre:
			if !known.genres[value] {
				return false
			}
		case settings.RuleLanguage:
			if !sameCode(known.language, value, languageCodes) {
				return false
			}
		case settings.RuleCountry:
			if !slices.ContainsFunc(known.countries, func(country string) bool { return sameCode(country, value, countryCodes) }) {
				return false
			}
		case settings.RuleAniList:
			if !known.aniList {
				return false
			}
		default:
			return false
		}
	}
	return len(rule.Conditions) > 0
}

// sameCode compares two ISO codes, a two-letter one equal to its three-letter one ("ja" and "jpn").
func sameCode(have, want string, longer map[string]string) bool {
	have, want = strings.ToLower(strings.TrimSpace(have)), strings.ToLower(want)
	return have != "" && (have == want || longer[want] == have || longer[have] == want)
}

// languageCodes and countryCodes are the three-letter codes of the two-letter ones rules are written in.
var (
	languageCodes = map[string]string{"ja": "jpn", "en": "eng", "ko": "kor", "zh": "zho", "de": "deu", "fr": "fra", "es": "spa", "it": "ita"}
	countryCodes  = map[string]string{"jp": "jpn", "us": "usa", "gb": "gbr", "kr": "kor", "cn": "chn", "de": "deu", "fr": "fra"}
)

// reasonOf says why a rule matched, in its conditions' words: "Animation, language ja".
func reasonOf(conditions []settings.RuleCondition) string {
	parts := make([]string, len(conditions))
	for i, condition := range conditions {
		switch condition.Field {
		case settings.RuleGenre:
			parts[i] = condition.Value
		case settings.RuleLanguage:
			parts[i] = "original language " + condition.Value
		case settings.RuleCountry:
			parts[i] = "from " + strings.ToUpper(condition.Value)
		case settings.RuleAniList:
			parts[i] = "on AniList"
		}
	}
	return strings.Join(parts, ", ")
}

// Check lists a library's works whose rules point to another library, and the kinds of library that do not exist yet
// although works it holds would go there (plan 10-libraries L5).
func (s Suggester) Check(ctx context.Context, categoryID int64) (apiserver.LibraryCheck, error) {
	snapshot := s.Settings.Snapshot()
	head := snapshot.LibraryOf(categoryID)
	var result apiserver.LibraryCheck
	missing := map[string]*apiserver.MissingKind{}
	for _, category := range snapshot.LibraryCategories(categoryID) {
		works, err := s.Works.TraitsInCategory(ctx, category.ID)
		if err != nil {
			return apiserver.LibraryCheck{}, err
		}
		own := s.rulesOf(snapshot, category)
		for _, work := range works {
			known := facts{
				genres: work.Genres, language: work.Language, aniList: work.AniList, ratingAge: work.RatingAge, ratingKnown: true,
			}
			if work.Country != "" {
				known.countries = []string{work.Country}
			}
			suggestion, score := s.best(snapshot, work.MediaType, known)
			if score > len(bestRule(own, known)) && snapshot.LibraryOf(suggestion.CategoryID) != head {
				result.Misplaced = append(result.Misplaced, apiserver.MisplacedWork{
					WorkID: work.WorkID, Title: work.Title, MediaType: work.MediaType, FromCategoryID: category.ID, Suggestion: suggestion,
				})
			}
			s.countMissingKinds(snapshot, work.MediaType, known, missing)
		}
	}
	for _, kind := range missing {
		result.MissingKinds = append(result.MissingKinds, *kind)
	}
	slices.SortFunc(result.MissingKinds, func(a, b apiserver.MissingKind) int { return int(b.Works - a.Works) })
	return result, nil
}

// countMissingKinds counts the work toward each kind of library its type ships rules for, matches, and has none of.
func (s Suggester) countMissingKinds(snapshot *settings.Snapshot, mediaType string, known facts, missing map[string]*apiserver.MissingKind) {
	descriptor, found := s.descriptor(mediaType)
	if !found {
		return
	}
	for _, kind := range descriptor.Kinds {
		if len(kind.Rules) == 0 || bestRule(kind.Rules, known) == nil || s.hasKind(snapshot, mediaType, kind.Key) {
			continue
		}
		key := mediaType + "/" + kind.Key
		if missing[key] == nil {
			missing[key] = &apiserver.MissingKind{MediaType: mediaType, Kind: kind.Key, Label: kind.Label}
		}
		missing[key].Works++
	}
}

// hasKind says a library of the type and kind exists, its own kind or its library's.
func (s Suggester) hasKind(snapshot *settings.Snapshot, mediaType, kind string) bool {
	for _, category := range snapshot.Categories() {
		if category.MediaType != mediaType {
			continue
		}
		head, _ := snapshot.Category(snapshot.LibraryOf(category.ID))
		if category.Kind == kind || head.Kind == kind {
			return true
		}
	}
	return false
}
