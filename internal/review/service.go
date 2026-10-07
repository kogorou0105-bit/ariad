// Package review owns the human handoff queue and human replies.
package review

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"ariad/internal/conversation"
	"ariad/internal/runtime"
)

type Status string

const (
	StatusOngoing  Status = "ongoing"
	StatusPending  Status = "pending"
	StatusResolved Status = "resolved"
)

var (
	ErrConversationNotFound = errors.New("conversation not found")
	ErrNotPending           = errors.New("conversation is not pending human review")
	ErrReplyRequired        = errors.New("reply text is required")
)

type State struct {
	WorkspaceID, ConversationID, VisitorID string
	Reason, RequestedBy                    string
	Status                                 Status
	RequestedAt                            time.Time
	ResolvedBy                             string
	ResolvedAt                             *time.Time
	UpdatedAt                              time.Time
}

type QueueItem struct {
	ConversationID, VisitorID, Reason, RequestedBy, LastMessageText string
	RequestedAt, LastActivityAt                                     time.Time
}

type HumanReply struct {
	ID, ConversationID, VisitorID, AuthorID, Text string
	CreatedAt                                     time.Time
}

type Repository interface {
	ConversationOwned(ctx context.Context, workspaceID, conversationID, visitorID string) (bool, error)
	MarkPending(ctx context.Context, state State) error
	GetState(ctx context.Context, workspaceID, conversationID, visitorID string) (State, bool, error)
	ListPending(ctx context.Context, workspaceID string) ([]QueueItem, error)
	SaveReply(ctx context.Context, workspaceID string, reply HumanReply) error
	ListReplies(ctx context.Context, workspaceID, conversationID, visitorID string) ([]HumanReply, error)
	Resolve(ctx context.Context, workspaceID, conversationID, resolvedBy string, resolvedAt time.Time) (bool, error)
	ReopenResolved(ctx context.Context, workspaceID, conversationID, visitorID string, updatedAt time.Time) error
}

type Service struct {
	repository Repository
	clock      func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (s *Service) Request(ctx context.Context, workspaceID, conversationID, visitorID, reason, requestedBy string) (State, error) {
	owned, err := s.repository.ConversationOwned(ctx, workspaceID, conversationID, visitorID)
	if err != nil {
		return State{}, fmt.Errorf("validate conversation: %w", err)
	}
	if !owned {
		return State{}, ErrConversationNotFound
	}
	now := s.clock().UTC()
	state := State{WorkspaceID: workspaceID, ConversationID: conversationID, VisitorID: visitorID, Status: StatusPending, Reason: strings.TrimSpace(reason), RequestedBy: requestedBy, RequestedAt: now, UpdatedAt: now}
	if state.Reason == "" {
		state.Reason = "visitor_requested"
	}
	if err := s.repository.MarkPending(ctx, state); err != nil {
		return State{}, fmt.Errorf("mark conversation pending: %w", err)
	}
	return state, nil
}

func (s *Service) MarkPending(ctx context.Context, workspaceID, conversationID, visitorID, reason, requestedBy string) error {
	_, err := s.Request(ctx, workspaceID, conversationID, visitorID, reason, requestedBy)
	return err
}

func (s *Service) Get(ctx context.Context, workspaceID, conversationID, visitorID string) (State, []HumanReply, error) {
	owned, err := s.repository.ConversationOwned(ctx, workspaceID, conversationID, visitorID)
	if err != nil || !owned {
		if err != nil {
			return State{}, nil, err
		}
		return State{}, nil, ErrConversationNotFound
	}
	state, found, err := s.repository.GetState(ctx, workspaceID, conversationID, visitorID)
	if err != nil {
		return State{}, nil, err
	}
	if !found {
		state = State{WorkspaceID: workspaceID, ConversationID: conversationID, VisitorID: visitorID, Status: StatusOngoing}
	}
	replies, err := s.repository.ListReplies(ctx, workspaceID, conversationID, visitorID)
	return state, replies, err
}

func (s *Service) ListPending(ctx context.Context, workspaceID string) ([]QueueItem, error) {
	return s.repository.ListPending(ctx, workspaceID)
}

func (s *Service) Reply(ctx context.Context, workspaceID, conversationID, visitorID, authorID, text string) (HumanReply, error) {
	if strings.TrimSpace(text) == "" {
		return HumanReply{}, ErrReplyRequired
	}
	owned, err := s.repository.ConversationOwned(ctx, workspaceID, conversationID, visitorID)
	if err != nil {
		return HumanReply{}, err
	}
	if !owned {
		return HumanReply{}, ErrConversationNotFound
	}
	state, found, err := s.repository.GetState(ctx, workspaceID, conversationID, visitorID)
	if err != nil {
		return HumanReply{}, err
	}
	if !found {
		return HumanReply{}, ErrNotPending
	}
	if state.Status != StatusPending {
		return HumanReply{}, ErrNotPending
	}
	id, err := randomID("human")
	if err != nil {
		return HumanReply{}, err
	}
	reply := HumanReply{ID: id, ConversationID: conversationID, VisitorID: visitorID, AuthorID: authorID, Text: strings.TrimSpace(text), CreatedAt: s.clock().UTC()}
	if err := s.repository.SaveReply(ctx, workspaceID, reply); err != nil {
		return HumanReply{}, err
	}
	return reply, nil
}

func (s *Service) Resolve(ctx context.Context, workspaceID, conversationID, visitorID, resolvedBy string) error {
	owned, err := s.repository.ConversationOwned(ctx, workspaceID, conversationID, visitorID)
	if err != nil {
		return fmt.Errorf("validate conversation: %w", err)
	}
	if !owned {
		return ErrConversationNotFound
	}
	ok, err := s.repository.Resolve(ctx, workspaceID, conversationID, resolvedBy, s.clock().UTC())
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotPending
	}
	return nil
}

// RecordConversationTurn applies the state transition caused by a newly saved turn.
// Handoff enters the queue; another new turn reopens a resolved conversation.
func (s *Service) RecordConversationTurn(ctx context.Context, event conversation.StateEvent) error {
	if event.Disposition == runtime.DispositionHandoff {
		return s.MarkPending(ctx, event.WorkspaceID, event.ConversationID, event.VisitorID, "ai_handoff", "system")
	}
	return s.repository.ReopenResolved(ctx, event.WorkspaceID, event.ConversationID, event.VisitorID, event.OccurredAt)
}

func randomID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}
