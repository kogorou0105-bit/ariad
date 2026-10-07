package visitor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSessionLifecycleAndExpiry(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository, time.Hour)
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }
	token, refreshToken, session, err := service.Create(context.Background(), "ws")
	if err != nil || token == "" {
		t.Fatalf("create = %#v %v", session, err)
	}
	identity, err := service.Authenticate(context.Background(), token)
	if err != nil || identity.VisitorID != session.Identity.VisitorID {
		t.Fatalf("authenticate = %#v %v", identity, err)
	}
	if _, err = service.Authenticate(context.Background(), "invalid"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("invalid = %v", err)
	}
	now = now.Add(time.Hour + time.Nanosecond)
	if _, err = service.Authenticate(context.Background(), token); !errors.Is(err, ErrExpiredSession) {
		t.Fatalf("expired = %v", err)
	}
	newToken, newRefreshToken, refreshed, err := service.Refresh(context.Background(), refreshToken)
	if err != nil || newToken == token || newRefreshToken == refreshToken || refreshed.Identity.VisitorID != session.Identity.VisitorID {
		t.Fatalf("refresh = %q %q %#v %v", newToken, newRefreshToken, refreshed, err)
	}
	if _, _, _, err = service.Refresh(context.Background(), refreshToken); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("reused refresh error = %v", err)
	}
	if _, err = service.Authenticate(context.Background(), newToken); err != nil {
		t.Fatalf("authenticate refreshed = %v", err)
	}
	if len(repository.sessions) != 1 {
		t.Fatalf("sessions after cleanup = %d, want 1", len(repository.sessions))
	}
}

func TestConcurrentAuthentication(t *testing.T) {
	service := NewService(NewMemoryRepository(), time.Hour)
	token, _, _, err := service.Create(context.Background(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := service.Authenticate(context.Background(), token); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
}
