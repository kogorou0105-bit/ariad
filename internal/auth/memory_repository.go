package auth

import (
	"context"
	"crypto/sha256"
	"sort"
	"sync"
	"time"
)

type memoryAccount struct {
	administrator Administrator
	passwordHash  []byte
}
type MemoryRepository struct {
	mu       sync.RWMutex
	accounts map[string]memoryAccount
	sessions map[[sha256.Size]byte]Session
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{accounts: make(map[string]memoryAccount), sessions: make(map[[sha256.Size]byte]Session)}
}
func (r *MemoryRepository) CountAdministrators(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int64(len(r.accounts)), nil
}
func (r *MemoryRepository) CreateAdministrator(ctx context.Context, a Administrator, h []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.accounts[a.Username]; ok {
		return ErrUsernameExists
	}
	r.accounts[a.Username] = memoryAccount{a, append([]byte(nil), h...)}
	return nil
}
func (r *MemoryRepository) FindAdministratorByUsername(ctx context.Context, u string) (Administrator, []byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return Administrator{}, nil, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.accounts[u]
	return a.administrator, append([]byte(nil), a.passwordHash...), ok, nil
}
func (r *MemoryRepository) FindAdministratorByID(ctx context.Context, id string) (Administrator, bool, error) {
	if err := ctx.Err(); err != nil {
		return Administrator{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.accounts {
		if a.administrator.ID == id {
			return a.administrator, true, nil
		}
	}
	return Administrator{}, false, nil
}
func (r *MemoryRepository) ListAdministrators(ctx context.Context) ([]Administrator, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Administrator, 0, len(r.accounts))
	for _, account := range r.accounts {
		result = append(result, account.administrator)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Username < result[j].Username })
	return result, nil
}
func (r *MemoryRepository) UpdatePassword(ctx context.Context, id string, hash []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for username, account := range r.accounts {
		if account.administrator.ID == id {
			account.passwordHash = append([]byte(nil), hash...)
			r.accounts[username] = account
			return nil
		}
	}
	return ErrInvalidCredentials
}
func (r *MemoryRepository) DeleteAdministratorIfNotLast(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.accounts) <= 1 {
		for _, account := range r.accounts {
			if account.administrator.ID == id {
				return false, nil
			}
		}
		return false, ErrAdministratorNotFound
	}
	for username, account := range r.accounts {
		if account.administrator.ID == id {
			delete(r.accounts, username)
			for token, session := range r.sessions {
				if session.AdministratorID == id {
					delete(r.sessions, token)
				}
			}
			return true, nil
		}
	}
	return false, ErrAdministratorNotFound
}
func (r *MemoryRepository) CreateSession(ctx context.Context, s Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.TokenHash] = s
	return nil
}
func (r *MemoryRepository) FindSession(ctx context.Context, h [sha256.Size]byte, now time.Time) (Session, bool, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[h]
	return s, ok && now.Before(s.ExpiresAt), nil
}
func (r *MemoryRepository) DeleteSession(ctx context.Context, h [sha256.Size]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, h)
	return nil
}
func (r *MemoryRepository) DeleteAdministratorSessions(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for token, session := range r.sessions {
		if session.AdministratorID == id {
			delete(r.sessions, token)
		}
	}
	return nil
}
func (r *MemoryRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for token, session := range r.sessions {
		if !now.Before(session.ExpiresAt) {
			delete(r.sessions, token)
		}
	}
	return nil
}
