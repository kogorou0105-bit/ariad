// Package visitor owns anonymous visitor identities and their expiring credentials.
package visitor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidSession = errors.New("visitor session is invalid")
	ErrExpiredSession = errors.New("visitor session has expired")
)

const DefaultSessionTTL = 30 * 24 * time.Hour

type Identity struct {
	WorkspaceID string
	VisitorID   string
	FirstSeenAt time.Time
	RefreshHash []byte
}

type Session struct {
	TokenHash  []byte
	Identity   Identity
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

type Profile struct {
	VisitorID         string
	FirstSeenAt       time.Time
	ConversationCount int64
	TotalTurnCount    int64
	LastActivityAt    time.Time
}

type Repository interface {
	CreateSession(context.Context, Session) error
	FindSession(context.Context, []byte) (Session, bool, error)
	RotateSession(context.Context, []byte, []byte, []byte, time.Time, time.Time) (Session, bool, error)
	TouchSession(context.Context, []byte, time.Time, time.Time) error
	DeleteExpiredSessions(context.Context, time.Time) error
	ListProfiles(context.Context, string) ([]Profile, error)
}

type Service struct {
	repository Repository
	ttl        time.Duration
	clock      func() time.Time
}

func NewService(repository Repository, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &Service{repository: repository, ttl: ttl, clock: time.Now}
}

func (s *Service) Create(ctx context.Context, workspaceID string) (string, string, Session, error) {
	if workspaceID == "" {
		return "", "", Session{}, errors.New("workspace is required")
	}
	now := s.clock().UTC()
	if err := s.repository.DeleteExpiredSessions(ctx, now); err != nil {
		return "", "", Session{}, fmt.Errorf("clean expired visitor sessions: %w", err)
	}
	visitorID, err := randomID("visitor")
	if err != nil {
		return "", "", Session{}, err
	}
	token, err := randomID("vst")
	if err != nil {
		return "", "", Session{}, err
	}
	refreshToken, err := randomID("vrf")
	if err != nil {
		return "", "", Session{}, err
	}
	session := Session{
		TokenHash: tokenHash(token),
		Identity:  Identity{WorkspaceID: workspaceID, VisitorID: visitorID, FirstSeenAt: now, RefreshHash: tokenHash(refreshToken)},
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(s.ttl),
	}
	if err := s.repository.CreateSession(ctx, session); err != nil {
		return "", "", Session{}, fmt.Errorf("create visitor session: %w", err)
	}
	return token, refreshToken, session, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (string, string, Session, error) {
	if refreshToken == "" {
		return "", "", Session{}, ErrInvalidSession
	}
	now := s.clock().UTC()
	if err := s.repository.DeleteExpiredSessions(ctx, now); err != nil {
		return "", "", Session{}, fmt.Errorf("clean expired visitor sessions: %w", err)
	}
	token, err := randomID("vst")
	if err != nil {
		return "", "", Session{}, err
	}
	newRefreshToken, err := randomID("vrf")
	if err != nil {
		return "", "", Session{}, err
	}
	session, found, err := s.repository.RotateSession(ctx, tokenHash(refreshToken), tokenHash(newRefreshToken), tokenHash(token), now, now.Add(s.ttl))
	if err != nil {
		return "", "", Session{}, fmt.Errorf("rotate visitor session: %w", err)
	}
	if !found {
		return "", "", Session{}, ErrInvalidSession
	}
	return token, newRefreshToken, session, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrInvalidSession
	}
	hash := tokenHash(token)
	session, found, err := s.repository.FindSession(ctx, hash)
	if err != nil {
		return Identity{}, fmt.Errorf("find visitor session: %w", err)
	}
	if !found {
		return Identity{}, ErrInvalidSession
	}
	now := s.clock().UTC()
	if !session.ExpiresAt.After(now) {
		return Identity{}, ErrExpiredSession
	}
	if err := s.repository.TouchSession(ctx, hash, now, now.Add(s.ttl)); err != nil {
		return Identity{}, fmt.Errorf("touch visitor session: %w", err)
	}
	return session.Identity, nil
}

func (s *Service) ListProfiles(ctx context.Context, workspaceID string) ([]Profile, error) {
	if workspaceID == "" {
		return nil, errors.New("workspace is required")
	}
	return s.repository.ListProfiles(ctx, workspaceID)
}

func tokenHash(token string) []byte { value := sha256.Sum256([]byte(token)); return value[:] }

func randomID(prefix string) (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate visitor identifier: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(value), nil
}
