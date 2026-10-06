package agent

import (
	"context"
	"errors"
	"fmt"
)

// ErrPublishedAgentNotFound indicates that an agent is not published in a workspace.
var ErrPublishedAgentNotFound = errors.New("published agent not found")

// PublishedAgent is the immutable agent configuration used for an answer turn.
type PublishedAgent struct {
	WorkspaceID  string
	AgentID      string
	VersionID    string
	Name         string
	Instructions string
}

// Reader resolves published agents within an explicit workspace boundary.
type Reader interface {
	GetPublished(ctx context.Context, workspaceID, agentID string) (PublishedAgent, error)
}

// StaticReader stores immutable development agent configurations in memory.
type StaticReader struct {
	agents map[string]map[string]PublishedAgent
}

// NewStaticReader creates an in-memory published-agent reader.
func NewStaticReader(agents []PublishedAgent) *StaticReader {
	byWorkspace := make(map[string]map[string]PublishedAgent)
	for _, published := range agents {
		if _, ok := byWorkspace[published.WorkspaceID]; !ok {
			byWorkspace[published.WorkspaceID] = make(map[string]PublishedAgent)
		}
		byWorkspace[published.WorkspaceID][published.AgentID] = published
	}

	return &StaticReader{agents: byWorkspace}
}

// GetPublished returns an agent only when both workspace and agent identifiers match.
func (r *StaticReader) GetPublished(
	ctx context.Context,
	workspaceID string,
	agentID string,
) (PublishedAgent, error) {
	if err := ctx.Err(); err != nil {
		return PublishedAgent{}, fmt.Errorf("read published agent: %w", err)
	}

	published, ok := r.agents[workspaceID][agentID]
	if !ok {
		return PublishedAgent{}, ErrPublishedAgentNotFound
	}
	return published, nil
}
