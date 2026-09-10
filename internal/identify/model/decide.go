package model

// UnitDecision is ADR-48's DECIDE step outcome. Values match migration 0018's
// import_unit.decision column exactly, so a future caller can persist this
// directly with no translation layer.
type UnitDecision string

const (
	// DecisionCommit is the chain resolved (Best is set) -- confident enough to
	// import automatically. What "confident enough" means is entirely
	// ChainConfig.AmbiguityThreshold's job (import.auto_commit_confidence, for
	// an import caller); Decide adds no second threshold of its own.
	DecisionCommit UnitDecision = "COMMIT"
	// DecisionQueue is nothing cleared AmbiguityThreshold -- route to F29 rather
	// than guess.
	DecisionQueue UnitDecision = "QUEUE"
	// DecisionSkip is an exclusion_pattern refuse-action file poisoning the
	// whole unit (ADR-53). Not produced by Decide -- CLASSIFY does not consult
	// exclusion_pattern yet (the still-open "import gate" item), so no caller
	// can reach this value today. Kept here only so the enum already matches
	// the schema it will eventually be written into.
	DecisionSkip UnitDecision = "SKIP"
	// DecisionFailed marks a unit where every file could not even be read. Not
	// produced by Decide either -- it is a caller's own IdentifyUnit error
	// path, not a property of a ChainResult, which by definition only exists
	// when identification ran to completion.
	DecisionFailed UnitDecision = "FAILED"
)

// Decide is ADR-48's DECIDE step, the confidence half: whether IDENTIFY's result
// commits automatically or goes to the manual identification queue. This is
// exactly "did Best get set", already decided by RunChain/RunChainForFiles against
// whatever AmbiguityThreshold the caller configured -- Decide exists so a caller
// has one named place to ask the question, not a second comparison to keep in sync
// with the chain's own.
func Decide(result ChainResult) UnitDecision {
	if result.Best != nil {
		return DecisionCommit
	}
	return DecisionQueue
}
