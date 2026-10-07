// Package auth owns administrator accounts and expiring sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials    = errors.New("invalid administrator credentials")
	ErrInvalidSession        = errors.New("invalid or expired administrator session")
	ErrUsernameRequired      = errors.New("username is required")
	ErrPasswordTooShort      = errors.New("password must be at least 12 characters")
	ErrUsernameExists        = errors.New("administrator username already exists")
	ErrTooManyAttempts       = errors.New("too many login attempts; try again later")
	ErrCannotDeleteSelf      = errors.New("cannot delete the current administrator")
	ErrLastAdministrator     = errors.New("cannot delete the last administrator")
	ErrAdministratorNotFound = errors.New("administrator not found")
)

const (
	DefaultSessionTTL    = 24 * time.Hour
	maximumLoginFailures = 5
	loginLockDuration    = 15 * time.Minute
)

var dummyPasswordHash = []byte("$2a$10$7EqJtq98hPqEX7fNZaFWoO5E.7z.b6xJtQxYgVub4X1g8B7IYjPti")

type Administrator struct {
	ID, Username string
	CreatedAt    time.Time
}
type Session struct {
	TokenHash            [sha256.Size]byte
	AdministratorID      string
	ExpiresAt, CreatedAt time.Time
}

type Repository interface {
	CountAdministrators(context.Context) (int64, error)
	CreateAdministrator(context.Context, Administrator, []byte) error
	FindAdministratorByUsername(context.Context, string) (Administrator, []byte, bool, error)
	FindAdministratorByID(context.Context, string) (Administrator, bool, error)
	ListAdministrators(context.Context) ([]Administrator, error)
	UpdatePassword(context.Context, string, []byte) error
	DeleteAdministratorIfNotLast(context.Context, string) (bool, error)
	CreateSession(context.Context, Session) error
	FindSession(context.Context, [sha256.Size]byte, time.Time) (Session, bool, error)
	DeleteSession(context.Context, [sha256.Size]byte) error
	DeleteAdministratorSessions(context.Context, string) error
	DeleteExpiredSessions(context.Context, time.Time) error
}

type loginFailure struct {
	count       int
	lockedUntil time.Time
}

type Service struct {
	repository Repository
	sessionTTL time.Duration
	clock      func() time.Time
	failures   map[string]loginFailure
	failureMu  sync.Mutex
}

func NewService(repository Repository, sessionTTL time.Duration) *Service {
	if sessionTTL <= 0 {
		sessionTTL = DefaultSessionTTL
	}
	return &Service{repository: repository, sessionTTL: sessionTTL, clock: time.Now, failures: make(map[string]loginFailure)}
}

// Bootstrap creates the first administrator only when no accounts exist.
// A non-empty password is intended for deterministic tests; production passes empty.
func (s *Service) Bootstrap(ctx context.Context, password string) (Administrator, string, bool, error) {
	count, err := s.repository.CountAdministrators(ctx)
	if err != nil {
		return Administrator{}, "", false, fmt.Errorf("count administrators: %w", err)
	}
	if count != 0 {
		return Administrator{}, "", false, nil
	}
	if password == "" {
		password, err = randomSecret(24)
		if err != nil {
			return Administrator{}, "", false, err
		}
	}
	administrator, err := s.create(ctx, "admin", password)
	if errors.Is(err, ErrUsernameExists) {
		return Administrator{}, "", false, nil
	}
	return administrator, password, err == nil, err
}

func (s *Service) CreateAdministrator(ctx context.Context, username, password string) (Administrator, error) {
	return s.create(ctx, username, password)
}

func (s *Service) create(ctx context.Context, username, password string) (Administrator, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return Administrator{}, ErrUsernameRequired
	}
	if len(password) < 12 {
		return Administrator{}, ErrPasswordTooShort
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Administrator{}, fmt.Errorf("hash administrator password: %w", err)
	}
	id, err := randomSecret(18)
	if err != nil {
		return Administrator{}, err
	}
	administrator := Administrator{ID: "admin_" + id, Username: username, CreatedAt: s.clock().UTC()}
	if err := s.repository.CreateAdministrator(ctx, administrator, hash); err != nil {
		return Administrator{}, err
	}
	return administrator, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (string, Administrator, time.Time, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	now := s.clock().UTC()
	if s.loginLocked(username, now) {
		return "", Administrator{}, time.Time{}, ErrTooManyAttempts
	}
	administrator, hash, found, err := s.repository.FindAdministratorByUsername(ctx, username)
	if err != nil {
		return "", Administrator{}, time.Time{}, err
	}
	comparisonHash := hash
	if !found {
		comparisonHash = dummyPasswordHash
	}
	if bcrypt.CompareHashAndPassword(comparisonHash, []byte(password)) != nil || !found {
		s.recordLoginFailure(username, now)
		return "", Administrator{}, time.Time{}, ErrInvalidCredentials
	}
	s.clearLoginFailures(username)
	token, err := randomSecret(32)
	if err != nil {
		return "", Administrator{}, time.Time{}, err
	}
	expiresAt := now.Add(s.sessionTTL)
	if err := s.repository.DeleteExpiredSessions(ctx, now); err != nil {
		return "", Administrator{}, time.Time{}, err
	}
	if err := s.repository.CreateSession(ctx, Session{TokenHash: sha256.Sum256([]byte(token)), AdministratorID: administrator.ID, CreatedAt: now, ExpiresAt: expiresAt}); err != nil {
		return "", Administrator{}, time.Time{}, err
	}
	return token, administrator, expiresAt, nil
}

func (s *Service) ListAdministrators(ctx context.Context) ([]Administrator, error) {
	return s.repository.ListAdministrators(ctx)
}

func (s *Service) ChangePassword(ctx context.Context, administratorID, currentPassword, newPassword string) error {
	if len(newPassword) < 12 {
		return ErrPasswordTooShort
	}
	administrator, found, err := s.repository.FindAdministratorByID(ctx, administratorID)
	if err != nil {
		return err
	}
	if !found {
		return ErrInvalidCredentials
	}
	_, hash, found, err := s.repository.FindAdministratorByUsername(ctx, administrator.Username)
	if err != nil {
		return err
	}
	if !found || bcrypt.CompareHashAndPassword(hash, []byte(currentPassword)) != nil {
		return ErrInvalidCredentials
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.repository.UpdatePassword(ctx, administratorID, newHash); err != nil {
		return err
	}
	return s.repository.DeleteAdministratorSessions(ctx, administratorID)
}

func (s *Service) DeleteAdministrator(ctx context.Context, currentAdministratorID, targetAdministratorID string) error {
	if currentAdministratorID == targetAdministratorID {
		return ErrCannotDeleteSelf
	}
	if _, found, err := s.repository.FindAdministratorByID(ctx, targetAdministratorID); err != nil {
		return err
	} else if !found {
		return ErrAdministratorNotFound
	}
	deleted, err := s.repository.DeleteAdministratorIfNotLast(ctx, targetAdministratorID)
	if err != nil {
		return err
	}
	if !deleted {
		if _, found, findErr := s.repository.FindAdministratorByID(ctx, targetAdministratorID); findErr != nil {
			return findErr
		} else if !found {
			return ErrAdministratorNotFound
		}
		return ErrLastAdministrator
	}
	return nil
}

func (s *Service) loginLocked(username string, now time.Time) bool {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	failure, found := s.failures[username]
	if !found {
		return false
	}
	if failure.lockedUntil.After(now) {
		return true
	}
	if !failure.lockedUntil.IsZero() {
		delete(s.failures, username)
	}
	return false
}
func (s *Service) recordLoginFailure(username string, now time.Time) {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	failure := s.failures[username]
	failure.count++
	if failure.count >= maximumLoginFailures {
		failure.lockedUntil = now.Add(loginLockDuration)
	}
	s.failures[username] = failure
}
func (s *Service) clearLoginFailures(username string) {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	delete(s.failures, username)
}

func (s *Service) Authenticate(ctx context.Context, token string) (Administrator, error) {
	if token == "" {
		return Administrator{}, ErrInvalidSession
	}
	session, found, err := s.repository.FindSession(ctx, sha256.Sum256([]byte(token)), s.clock().UTC())
	if err != nil {
		return Administrator{}, err
	}
	if !found {
		return Administrator{}, ErrInvalidSession
	}
	administrator, found, err := s.repository.FindAdministratorByID(ctx, session.AdministratorID)
	if err != nil {
		return Administrator{}, err
	}
	if !found {
		return Administrator{}, ErrInvalidSession
	}
	return administrator, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return ErrInvalidSession
	}
	return s.repository.DeleteSession(ctx, sha256.Sum256([]byte(token)))
}

func randomSecret(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate authentication secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
