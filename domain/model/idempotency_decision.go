package model

type BeginDecisionType string

const (
	DecisionAcquired   BeginDecisionType = "acquired"
	DecisionReplay     BeginDecisionType = "replay"
	DecisionConflict   BeginDecisionType = "conflict"
	DecisionInProgress BeginDecisionType = "in_progress"
	DecisionFailed     BeginDecisionType = "failed"
)

type BeginDecision struct {
	Type   BeginDecisionType
	Record *IdempotencyRecord
}

func Acquired(record *IdempotencyRecord) BeginDecision {
	return BeginDecision{Type: DecisionAcquired, Record: record}
}

func Replay(record *IdempotencyRecord) BeginDecision {
	return BeginDecision{Type: DecisionReplay, Record: record}
}

func Conflict(record *IdempotencyRecord) BeginDecision {
	return BeginDecision{Type: DecisionConflict, Record: record}
}

func InProgress(record *IdempotencyRecord) BeginDecision {
	return BeginDecision{Type: DecisionInProgress, Record: record}
}

func Failed(record *IdempotencyRecord) BeginDecision {
	return BeginDecision{Type: DecisionFailed, Record: record}
}
