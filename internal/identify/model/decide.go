package model

// UnitDecision is ADR-48's DECIDE step outcome. Values match migration 0018's
// import_unit.decision column exactly, so a future caller can persist this
// directly with no translation layer.
type UnitDecision string

const (
	// DecisionCommit is the chain resolved (Best set); how confident that must be is ChainConfig.AmbiguityThreshold's job, Decide adds no threshold.
	DecisionCommit UnitDecision = "COMMIT"
	// DecisionQueue is nothing cleared AmbiguityThreshold -- route to F29 rather
	// than guess.
	DecisionQueue UnitDecision = "QUEUE"
	// DecisionSkip is an exclusion-pattern refuse file or a blocked_content hash (ADR-53); set by the importjob caller with a structured Reason, never by Decide.
	DecisionSkip UnitDecision = "SKIP"
	// DecisionFailed is no file in the unit could be read; set by the caller's IdentifyUnit error path, not by Decide.
	DecisionFailed UnitDecision = "FAILED"
)

// import_unit.reason prefixes for the ADR-53 gates, matchable by strings.HasPrefix with detail after a colon; ReasonBlockedPattern pairs with DecisionSkip, ReasonBlockedContent can follow a commit decision at COMMIT time.
const (
	ReasonBlockedPattern = "BLOCKED_PATTERN"
	ReasonBlockedContent = "BLOCKED_CONTENT"
	// ReasonDiscStructure holds a Blu-ray or DVD folder for a person; it is never placed automatically.
	ReasonDiscStructure = "DISC_STRUCTURE"
	// ReasonIdentifying marks a unit the listing pass found whose files the identifying pass has not read yet.
	ReasonIdentifying = "IDENTIFYING"
)

// Decide is ADR-48's confidence half of DECIDE: commit if Best was set by the chain, else the manual queue; it exists so there is one named place to ask, not a second comparison.
func Decide(result ChainResult) UnitDecision {
	if result.Best != nil {
		return DecisionCommit
	}
	return DecisionQueue
}
