package visitor

import (
	"context"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

type MemoryRepository struct {
	mu         sync.RWMutex
	sessions   map[string]Session
	identities map[string]Identity
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{sessions: make(map[string]Session), identities: make(map[string]Identity)}
}

func (r *MemoryRepository) CreateSession(ctx context.Context, session Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.identities[hex.EncodeToString(session.Identity.RefreshHash)] = session.Identity
	r.sessions[hex.EncodeToString(session.TokenHash)] = session
	return nil
}

func (r *MemoryRepository) RotateSession(ctx context.Context, oldRefreshHash, newRefreshHash, tokenHash []byte, now, expiresAt time.Time) (Session, bool, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	oldKey := hex.EncodeToString(oldRefreshHash)
	identity, found := r.identities[oldKey]
	if !found {
		return Session{}, false, nil
	}
	delete(r.identities, oldKey)
	identity.RefreshHash = append([]byte(nil), newRefreshHash...)
	r.identities[hex.EncodeToString(newRefreshHash)] = identity
	session := Session{TokenHash: append([]byte(nil), tokenHash...), Identity: identity, CreatedAt: now, LastSeenAt: now, ExpiresAt: expiresAt}
	r.sessions[hex.EncodeToString(tokenHash)] = session
	return session, true, nil
}

func (r *MemoryRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, session := range r.sessions {
		if !session.ExpiresAt.After(now) {
			delete(r.sessions, key)
		}
	}
	return nil
}

func (r *MemoryRepository) FindSession(ctx context.Context, hash []byte) (Session, bool, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, found := r.sessions[hex.EncodeToString(hash)]
	return session, found, nil
}

func (r *MemoryRepository) TouchSession(ctx context.Context, hash []byte, seenAt, expiresAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := hex.EncodeToString(hash)
	session, found := r.sessions[key]
	if !found {
		return ErrInvalidSession
	}
	session.LastSeenAt, session.ExpiresAt = seenAt, expiresAt
	r.sessions[key] = session
	return nil
}

func (r *MemoryRepository) ListProfiles(ctx context.Context, workspaceID string) ([]Profile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	profiles := map[string]Profile{}
	for _, identity := range r.identities {
		if identity.WorkspaceID != workspaceID {
			continue
		}
		profile, found := profiles[identity.VisitorID]
		if !found {
			profile = Profile{VisitorID: identity.VisitorID, FirstSeenAt: identity.FirstSeenAt, LastActivityAt: identity.FirstSeenAt}
		}
		profiles[profile.VisitorID] = profile
	}
	for _, session := range r.sessions {
		profile, found := profiles[session.Identity.VisitorID]
		if found && session.LastSeenAt.After(profile.LastActivityAt) {
			profile.LastActivityAt = session.LastSeenAt
			profiles[profile.VisitorID] = profile
		}
	}
	result := make([]Profile, 0, len(profiles))
	for _, profile := range profiles {
		result = append(result, profile)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastActivityAt.After(result[j].LastActivityAt) })
	return result, nil
}
