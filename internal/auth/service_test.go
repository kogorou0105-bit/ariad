package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLoginAuthenticateLogoutAndExpiry(t *testing.T) {
	t.Parallel()
	service := NewService(NewMemoryRepository(), time.Hour)
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }
	administrator, password, created, err := service.Bootstrap(context.Background(), "correct-password")
	if err != nil || !created || administrator.Username != "admin" {
		t.Fatalf("bootstrap = %#v, %q, %v, %v", administrator, password, created, err)
	}
	if _, _, createdAgain, bootstrapErr := service.Bootstrap(context.Background(), "different-password"); bootstrapErr != nil || createdAgain {
		t.Fatalf("second bootstrap created = %v, error = %v", createdAgain, bootstrapErr)
	}
	if _, _, _, err := service.Login(context.Background(), "admin", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	token, loggedIn, expiresAt, err := service.Login(context.Background(), "ADMIN", password)
	if err != nil || loggedIn.ID != administrator.ID || !expiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("login = %q %#v %v %v", token, loggedIn, expiresAt, err)
	}
	if _, err := service.Authenticate(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	now = expiresAt
	if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired error = %v", err)
	}
	now = expiresAt.Add(-time.Minute)
	if err := service.Logout(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("logout error = %v", err)
	}
}

func TestCreateAdministratorStoresPasswordHash(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository, time.Hour)
	administrator, err := service.CreateAdministrator(context.Background(), " Operator ", "long-enough-password")
	if err != nil || administrator.Username != "operator" {
		t.Fatalf("administrator = %#v err = %v", administrator, err)
	}
	_, hash, found, err := repository.FindAdministratorByUsername(context.Background(), "operator")
	if err != nil || !found || string(hash) == "long-enough-password" {
		t.Fatalf("password was not hashed: found=%v err=%v hash=%q", found, err, hash)
	}
}

func TestLoginRateLimitAndAdministratorLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service := NewService(NewMemoryRepository(), time.Hour)
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }
	administrator, password, _, err := service.Bootstrap(ctx, "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < maximumLoginFailures; attempt++ {
		if _, _, _, err = service.Login(ctx, administrator.Username, "wrong"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
	}
	if _, _, _, err = service.Login(ctx, administrator.Username, "wrong"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("rate limit error = %v", err)
	}
	now = now.Add(loginLockDuration)
	if _, _, _, err = service.Login(ctx, administrator.Username, password); err != nil {
		t.Fatalf("login after lock expiry error = %v", err)
	}
	second, err := service.CreateAdministrator(ctx, "second", "second-password")
	if err != nil {
		t.Fatal(err)
	}
	items, err := service.ListAdministrators(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("administrators = %#v err = %v", items, err)
	}
	token, _, _, err := service.Login(ctx, administrator.Username, password)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ChangePassword(ctx, administrator.ID, password, "changed-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(ctx, token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("old session error = %v", err)
	}
	if _, _, _, err = service.Login(ctx, administrator.Username, "changed-password"); err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteAdministrator(ctx, administrator.ID, administrator.ID); !errors.Is(err, ErrCannotDeleteSelf) {
		t.Fatalf("delete self error = %v", err)
	}
	if err = service.DeleteAdministrator(ctx, administrator.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteAdministrator(ctx, administrator.ID, "missing"); !errors.Is(err, ErrAdministratorNotFound) {
		t.Fatalf("delete missing error = %v", err)
	}
	if err = service.DeleteAdministrator(ctx, "other", administrator.ID); !errors.Is(err, ErrLastAdministrator) {
		t.Fatalf("delete last error = %v", err)
	}
}
