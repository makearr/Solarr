package screen

import "github.com/makearr/Solarr/internal/db/settings"

func condition(field settings.RuleField, value string) settings.RuleCondition {
	return settings.RuleCondition{Field: field, Value: value}
}

// The rules a film or TV library of a kind suggests itself by (plan 10-libraries L5): a work matching any of them.
var (
	AnimeRules = []settings.LibraryRule{
		{Conditions: []settings.RuleCondition{condition(settings.RuleGenre, "Animation"), condition(settings.RuleLanguage, "ja")}},
		{Conditions: []settings.RuleCondition{condition(settings.RuleGenre, "Animation"), condition(settings.RuleCountry, "JP")}},
		{Conditions: []settings.RuleCondition{condition(settings.RuleAniList, "")}},
	}
	DocumentaryRules = []settings.LibraryRule{{Conditions: []settings.RuleCondition{condition(settings.RuleGenre, "Documentary")}}}
	KidsRules        = []settings.LibraryRule{
		{Conditions: []settings.RuleCondition{condition(settings.RuleGenre, "Kids")}},
		{Conditions: []settings.RuleCondition{condition(settings.RuleGenre, "Family")}},
	}
	RealityRules = []settings.LibraryRule{{Conditions: []settings.RuleCondition{condition(settings.RuleGenre, "Reality")}}}
)
