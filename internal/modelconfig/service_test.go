package modelconfig

import (
	"context"
	"errors"
	"testing"
)

func TestServiceSaveUpdateResetAndMask(t *testing.T) {
	service := NewService(NewMemoryRepository(), Config{BaseURL: "https://default.example/v1", Model: "default", APIKey: "default-secret"})
	ctx := context.Background()
	if _, err := service.Save(ctx, SaveCommand{WorkspaceID: "ws", BaseURL: "://bad", Model: "x", APIKey: "secret"}); !errors.Is(err, ErrInvalidBaseURL) {
		t.Fatalf("invalid URL error = %v", err)
	}
	if _, err := service.Save(ctx, SaveCommand{WorkspaceID: "ws", BaseURL: "https://custom.example/v1", Model: "", APIKey: "secret"}); !errors.Is(err, ErrModelRequired) {
		t.Fatalf("missing model error = %v", err)
	}
	if _, err := service.Save(ctx, SaveCommand{WorkspaceID: "ws", BaseURL: "https://custom.example/v1", Model: "custom"}); !errors.Is(err, ErrAPIKeyRequired) {
		t.Fatalf("missing key error = %v", err)
	}
	status, err := service.Save(ctx, SaveCommand{WorkspaceID: "ws", BaseURL: "https://custom.example/v1/", Model: "custom", APIKey: "workspace-secret"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if status.Source != "workspace" || status.APIKeyMask == "workspace-secret" || status.APIKeyMask == "" {
		t.Fatalf("status = %#v", status)
	}
	if _, err := service.Save(ctx, SaveCommand{WorkspaceID: "ws", BaseURL: "https://new.example", Model: "new"}); err != nil {
		t.Fatalf("update keeping key: %v", err)
	}
	config, _, _ := service.Get(ctx, "ws")
	if config.APIKey != "workspace-secret" {
		t.Fatalf("key changed = %q", config.APIKey)
	}
	reset, err := service.Reset(ctx, "ws")
	if err != nil || reset.Source != "system_default" || reset.Model != "default" {
		t.Fatalf("reset = %#v, err = %v", reset, err)
	}
}

func TestMemoryRepositoryIsolatesWorkspaces(t *testing.T) {
	repository := NewMemoryRepository()
	_ = repository.Save(context.Background(), Config{WorkspaceID: "ws_one", APIKey: "one"})
	_, found, err := repository.Get(context.Background(), "ws_two")
	if err != nil || found {
		t.Fatalf("other workspace found = %v, err = %v", found, err)
	}
}
