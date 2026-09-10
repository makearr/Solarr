package model_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/identify/model"
)

// A resolved chain result commits; there is no second threshold to clear beyond
// what AmbiguityThreshold already decided when Best was set.
func TestDecide_CommitsWhenBestIsSet(t *testing.T) {
	result := model.ChainResult{Best: &model.Identification{Strategy: "embedded-tag", Confidence: 0.95}}
	if got := model.Decide(result); got != model.DecisionCommit {
		t.Errorf("Decide = %q, want COMMIT", got)
	}
}

// Nothing cleared the ambiguity threshold: queue, not skip or fail -- those are
// not this function's decisions to make.
func TestDecide_QueuesWhenBestIsNil(t *testing.T) {
	result := model.ChainResult{Candidates: []model.Identification{{Strategy: "parse", Confidence: 0.3}}}
	if got := model.Decide(result); got != model.DecisionQueue {
		t.Errorf("Decide = %q, want QUEUE", got)
	}
}

// The zero value (no strategy found anything at all) queues too, not some third
// outcome -- an empty ChainResult is indistinguishable from "found nothing".
func TestDecide_QueuesOnTheZeroValue(t *testing.T) {
	if got := model.Decide(model.ChainResult{}); got != model.DecisionQueue {
		t.Errorf("Decide = %q, want QUEUE", got)
	}
}
