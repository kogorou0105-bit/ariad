// Package usage defines the immutable usage-fact recording boundary.
package usage

import "time"

// Status describes whether a model invocation consumed provider units.
type Status string

const (
	// StatusCompleted indicates a completed model invocation.
	StatusCompleted Status = "completed"
	// StatusFailed indicates a failed model invocation.
	StatusFailed Status = "failed"
	// StatusNotInvoked indicates the turn terminated before calling a model.
	StatusNotInvoked Status = "not_invoked"
)

// Fact is a raw, immutable usage observation.
type Fact struct {
	DeduplicationKey string
	WorkspaceID      string
	AgentID          string
	RequestID        string
	AnswerID         string
	Provider         string
	Model            string
	InputUnits       int64
	CachedInputUnits int64
	OutputUnits      int64
	Status           Status
	OccurredAt       time.Time
}
