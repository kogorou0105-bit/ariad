// Package usage defines the immutable usage-fact recording boundary.
package usage

import (
	"context"
	"time"
)

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

// Recorder persists usage facts. Production implementations must not silently
// discard facts; the no-op implementation is only for local development/tests.
type Recorder interface {
	Record(ctx context.Context, fact Fact) error
}

// NoopRecorder intentionally discards local-development usage facts.
type NoopRecorder struct{}

// NewNoopRecorder creates the explicitly local no-op recorder.
func NewNoopRecorder() NoopRecorder {
	return NoopRecorder{}
}

// Record implements Recorder.
func (NoopRecorder) Record(context.Context, Fact) error {
	return nil
}
