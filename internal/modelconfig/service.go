// Package modelconfig owns workspace-scoped model provider configuration.
package modelconfig

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalidBaseURL     = errors.New("model base URL must be an absolute HTTP(S) URL without query or fragment")
	ErrModelRequired      = errors.New("model name is required")
	ErrAPIKeyRequired     = errors.New("API key is required when creating a workspace model configuration")
	ErrEncryptionDisabled = errors.New("workspace model configuration encryption is not configured")
)

type Config struct {
	WorkspaceID string
	BaseURL     string
	Model       string
	APIKey      string
	UpdatedAt   time.Time
}

type SaveCommand struct {
	WorkspaceID string
	BaseURL     string
	Model       string
	APIKey      string
}

type Status struct {
	WorkspaceID string
	Source      string
	BaseURL     string
	Model       string
	APIKeyMask  string
}

type Repository interface {
	Get(ctx context.Context, workspaceID string) (Config, bool, error)
	Save(ctx context.Context, config Config) error
	Delete(ctx context.Context, workspaceID string) error
}

type Reader interface {
	Get(ctx context.Context, workspaceID string) (Config, bool, error)
}

type Service struct {
	repository Repository
	fallback   Config
	clock      func() time.Time
}

func NewService(repository Repository, fallback Config) *Service {
	return &Service{repository: repository, fallback: fallback, clock: time.Now}
}

func (s *Service) Get(ctx context.Context, workspaceID string) (Config, bool, error) {
	return s.repository.Get(ctx, workspaceID)
}

func (s *Service) GetStatus(ctx context.Context, workspaceID string) (Status, error) {
	config, found, err := s.repository.Get(ctx, workspaceID)
	if err != nil && !errors.Is(err, ErrEncryptionDisabled) {
		return Status{}, fmt.Errorf("get workspace model configuration: %w", err)
	}
	if found {
		return statusFromConfig(workspaceID, "workspace", config), nil
	}
	source := "system_default"
	if strings.TrimSpace(s.fallback.APIKey) == "" {
		source = "local"
	}
	return statusFromConfig(workspaceID, source, s.fallback), nil
}

func (s *Service) Save(ctx context.Context, command SaveCommand) (Status, error) {
	baseURL, model, err := validate(command.BaseURL, command.Model)
	if err != nil {
		return Status{}, err
	}
	existing, found, err := s.repository.Get(ctx, command.WorkspaceID)
	if err != nil {
		return Status{}, fmt.Errorf("get existing workspace model configuration: %w", err)
	}
	apiKey := strings.TrimSpace(command.APIKey)
	if apiKey == "" {
		if !found {
			return Status{}, ErrAPIKeyRequired
		}
		apiKey = existing.APIKey
	}
	config := Config{WorkspaceID: command.WorkspaceID, BaseURL: baseURL, Model: model, APIKey: apiKey, UpdatedAt: s.clock().UTC()}
	if err := s.repository.Save(ctx, config); err != nil {
		return Status{}, fmt.Errorf("save workspace model configuration: %w", err)
	}
	return statusFromConfig(command.WorkspaceID, "workspace", config), nil
}

func (s *Service) Reset(ctx context.Context, workspaceID string) (Status, error) {
	if err := s.repository.Delete(ctx, workspaceID); err != nil {
		return Status{}, fmt.Errorf("reset workspace model configuration: %w", err)
	}
	return s.GetStatus(ctx, workspaceID)
}

func validate(rawBaseURL, rawModel string) (string, string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(rawBaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", ErrInvalidBaseURL
	}
	model := strings.TrimSpace(rawModel)
	if model == "" {
		return "", "", ErrModelRequired
	}
	return baseURL, model, nil
}

func statusFromConfig(workspaceID, source string, config Config) Status {
	return Status{WorkspaceID: workspaceID, Source: source, BaseURL: config.BaseURL, Model: config.Model, APIKeyMask: MaskAPIKey(config.APIKey)}
}

func MaskAPIKey(apiKey string) string {
	runes := []rune(strings.TrimSpace(apiKey))
	if len(runes) == 0 {
		return ""
	}
	if len(runes) <= 4 {
		return strings.Repeat("•", len(runes))
	}
	return "••••••••" + string(runes[len(runes)-4:])
}
