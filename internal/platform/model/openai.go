package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ariad/internal/modelconfig"
	"ariad/internal/runtime"
)

const (
	defaultModelBaseURL = "https://api.deepseek.com"
	defaultModelName    = "deepseek-chat"
	modelHTTPTimeout    = 25 * time.Second
	maximumRetries      = 2
)

// OpenAIConfig configures an OpenAI-compatible chat completions adapter.
type OpenAIConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

type EnvironmentSettings struct {
	BaseURL string
	Model   string
	APIKey  string
}

func SettingsFromEnvironment() EnvironmentSettings {
	settings := EnvironmentSettings{
		BaseURL: strings.TrimSpace(os.Getenv("ARIAD_MODEL_BASE_URL")),
		Model:   strings.TrimSpace(os.Getenv("ARIAD_MODEL_NAME")),
		APIKey:  strings.TrimSpace(os.Getenv("ARIAD_MODEL_API_KEY")),
	}
	if settings.BaseURL == "" {
		settings.BaseURL = defaultModelBaseURL
	}
	if settings.Model == "" {
		settings.Model = defaultModelName
	}
	return settings
}

// OpenAI calls an OpenAI-compatible chat completions endpoint.
type OpenAI struct {
	endpoint   string
	apiKey     string
	model      string
	provider   string
	httpClient *http.Client
}

var _ runtime.Model = (*OpenAI)(nil)

// NewFromEnvironment selects the local stub when no API key is configured and
// otherwise creates an OpenAI-compatible adapter. Secrets are read only here
// and are never included in errors or logs.
func NewFromEnvironment() (runtime.Model, error) {
	settings := SettingsFromEnvironment()
	if settings.APIKey == "" {
		return NewStub(), nil
	}
	return NewOpenAI(OpenAIConfig{BaseURL: settings.BaseURL, APIKey: settings.APIKey, Model: settings.Model})
}

// WorkspaceModel resolves a workspace override for every request, so saves and resets take effect immediately.
type WorkspaceModel struct {
	configs  modelconfig.Reader
	fallback runtime.Model
}

func NewWorkspaceModel(configs modelconfig.Reader, fallback runtime.Model) *WorkspaceModel {
	return &WorkspaceModel{configs: configs, fallback: fallback}
}

func (m *WorkspaceModel) Generate(ctx context.Context, request runtime.ModelRequest) (runtime.ModelResponse, error) {
	config, found, err := m.configs.Get(ctx, request.WorkspaceID)
	if err != nil {
		if errors.Is(err, modelconfig.ErrEncryptionDisabled) {
			return m.fallback.Generate(ctx, request)
		}
		return runtime.ModelResponse{}, fmt.Errorf("resolve workspace model: %w", err)
	}
	if !found {
		return m.fallback.Generate(ctx, request)
	}
	adapter, err := NewOpenAI(OpenAIConfig{BaseURL: config.BaseURL, APIKey: config.APIKey, Model: config.Model})
	if err != nil {
		return runtime.ModelResponse{}, fmt.Errorf("configure workspace model: %w", err)
	}
	return adapter.Generate(ctx, request)
}

// NewOpenAI validates configuration and creates an OpenAI-compatible adapter.
func NewOpenAI(config OpenAIConfig) (*OpenAI, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsedURL, err := url.Parse(baseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, errors.New("model base URL must be an absolute HTTP URL")
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, errors.New("model base URL must use HTTP or HTTPS")
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return nil, errors.New("model base URL must not contain a query or fragment")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("model API key is required")
	}
	modelName := strings.TrimSpace(config.Model)
	if modelName == "" {
		return nil, errors.New("model name is required")
	}

	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: modelHTTPTimeout}
	} else if client.Timeout <= 0 || client.Timeout > modelHTTPTimeout {
		cloned := *client
		cloned.Timeout = modelHTTPTimeout
		client = &cloned
	}

	return &OpenAI{
		endpoint:   baseURL + "/chat/completions",
		apiKey:     strings.TrimSpace(config.APIKey),
		model:      modelName,
		provider:   providerName(parsedURL),
		httpClient: client,
	}, nil
}

// Generate calls chat completions and returns generated text plus evidence IDs.
func (a *OpenAI) Generate(
	ctx context.Context,
	request runtime.ModelRequest,
) (runtime.ModelResponse, error) {
	payload, err := json.Marshal(chatCompletionRequest{
		Model: a.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt(request.Instructions, request.Locale)},
			{Role: "user", Content: userPrompt(request)},
		},
	})
	if err != nil {
		return runtime.ModelResponse{}, fmt.Errorf("encode model request: %w", err)
	}

	for attempt := 0; attempt <= maximumRetries; attempt++ {
		response, retry, requestErr := a.complete(ctx, payload)
		if requestErr == nil {
			return response, nil
		}
		if !retry || attempt == maximumRetries {
			return runtime.ModelResponse{}, requestErr
		}
		if err := waitForRetry(ctx, attempt); err != nil {
			return runtime.ModelResponse{}, fmt.Errorf("wait to retry model request: %w", err)
		}
	}

	return runtime.ModelResponse{}, errors.New("model request exhausted retries")
}

func (a *OpenAI) complete(
	ctx context.Context,
	payload []byte,
) (runtime.ModelResponse, bool, error) {
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.endpoint,
		bytes.NewReader(payload),
	)
	if err != nil {
		return runtime.ModelResponse{}, false, fmt.Errorf("create model request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := a.httpClient.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return runtime.ModelResponse{}, false, fmt.Errorf("call model: %w", ctx.Err())
		}
		return runtime.ModelResponse{}, true, fmt.Errorf("call model: %w", err)
	}
	defer func() {
		_ = httpResponse.Body.Close()
	}()

	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		message := readProviderErrorMessage(httpResponse.Body)
		retry := httpResponse.StatusCode == http.StatusTooManyRequests ||
			httpResponse.StatusCode >= http.StatusInternalServerError
		if message != "" {
			return runtime.ModelResponse{}, retry, fmt.Errorf(
				"model returned HTTP status %d: %s",
				httpResponse.StatusCode,
				message,
			)
		}
		return runtime.ModelResponse{}, retry, fmt.Errorf("model returned HTTP status %d", httpResponse.StatusCode)
	}

	var completion chatCompletionResponse
	decoder := json.NewDecoder(io.LimitReader(httpResponse.Body, 2<<20))
	if err := decoder.Decode(&completion); err != nil {
		return runtime.ModelResponse{}, false, fmt.Errorf("decode model response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return runtime.ModelResponse{}, false, errors.New("model response has no choices")
	}

	text, evidenceIDs := parseCitations(completion.Choices[0].Message.Content)
	actualModel := strings.TrimSpace(completion.Model)
	if actualModel == "" {
		actualModel = a.model
	}
	return runtime.ModelResponse{
		Text:             text,
		CitedEvidenceIDs: evidenceIDs,
		Usage: runtime.ModelUsage{
			Invoked:          true,
			Provider:         a.provider,
			Model:            actualModel,
			InputUnits:       completion.Usage.PromptTokens,
			CachedInputUnits: completion.Usage.PromptCacheHitTokens,
			OutputUnits:      completion.Usage.CompletionTokens,
		},
	}, false, nil
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			// DeepSeek may include reasoning_content. It is intentionally not modeled
			// because only message.content is part of the answer contract.
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens         int64 `json:"prompt_tokens"`
		PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
		CompletionTokens     int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func systemPrompt(instructions, locale string) string {
	languageRule := "Answer in the same language as the user's question."
	if normalizedLocale, ok := safeLocale(locale); ok {
		languageRule = "Answer using locale " + normalizedLocale + "."
	}
	return strings.TrimSpace(instructions) + "\n\n" + languageRule + `

Answer only from the evidence supplied in the user message. Do not invent facts.
Every evidence item begins with its allowed ID in square brackets.
At the end of the answer, output exactly one separate line in this form:
CITATIONS: ev_xxx, ev_yyy
List only the evidence IDs actually used. Never invent an evidence ID.
Do not output source IDs, quotes, or other citation metadata.`
}

func safeLocale(locale string) (string, bool) {
	normalized := strings.TrimSpace(locale)
	if normalized == "" || len(normalized) > 35 {
		return "", false
	}
	for _, character := range normalized {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return "", false
	}
	return normalized, true
}

func readProviderErrorMessage(body io.Reader) string {
	payload, err := io.ReadAll(io.LimitReader(body, 32<<10))
	if err != nil {
		return ""
	}
	var providerError struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &providerError); err != nil {
		return ""
	}
	message := strings.Join(strings.Fields(providerError.Error.Message), " ")
	runes := []rune(message)
	if len(runes) > 1_024 {
		message = string(runes[:1_024])
	}
	return message
}

func userPrompt(request runtime.ModelRequest) string {
	var prompt strings.Builder
	if len(request.History) > 0 {
		heading, rule, userLabel, assistantLabel := historyPromptLabels(request.Locale)
		prompt.WriteString(heading)
		prompt.WriteString("\n")
		prompt.WriteString(rule)
		prompt.WriteString("\n\n")
		for _, turn := range request.History {
			prompt.WriteString(userLabel)
			prompt.WriteString(turn.Question)
			prompt.WriteString("\n")
			prompt.WriteString(assistantLabel)
			prompt.WriteString(turn.Answer)
			prompt.WriteString("\n\n")
		}
	}
	prompt.WriteString("Question:\n")
	prompt.WriteString(request.Question)
	prompt.WriteString("\n\nEvidence:\n")
	for _, evidence := range request.Evidence {
		prompt.WriteString("[")
		prompt.WriteString(evidence.EvidenceID)
		prompt.WriteString("]\n")
		prompt.WriteString(evidence.Text)
		prompt.WriteString("\n\n")
	}
	return strings.TrimSpace(prompt.String())
}

func historyPromptLabels(
	locale string,
) (heading string, rule string, userLabel string, assistantLabel string) {
	if normalized, ok := safeLocale(locale); ok && strings.HasPrefix(strings.ToLower(normalized), "zh") {
		return "此前对话：", "以下是此前的对话，仅供参考；回答仍必须只依据本轮 Evidence。", "用户：", "助手："
	}
	return "Previous conversation:", "The following is prior conversation for reference only; " +
		"the answer must still rely exclusively on this turn's Evidence.", "User: ", "Assistant: "
}

func parseCitations(content string) (string, []string) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	citationLine := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.HasPrefix(strings.TrimSpace(lines[index]), "CITATIONS:") {
			citationLine = index
			break
		}
	}
	if citationLine < 0 {
		return strings.TrimSpace(content), []string{}
	}

	line := strings.TrimSpace(lines[citationLine])
	rawIDs := strings.TrimSpace(strings.TrimPrefix(line, "CITATIONS:"))
	lines = append(lines[:citationLine], lines[citationLine+1:]...)
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if rawIDs == "" {
		return text, []string{}
	}

	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, rawID := range strings.Split(rawIDs, ",") {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return text, []string{}
		}
		if _, duplicated := seen[id]; duplicated {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return text, ids
}

func providerName(baseURL *url.URL) string {
	hostname := strings.ToLower(baseURL.Hostname())
	switch {
	case strings.Contains(hostname, "deepseek"):
		return "deepseek"
	case strings.Contains(hostname, "openai"):
		return "openai"
	default:
		return hostname
	}
}

func waitForRetry(ctx context.Context, attempt int) error {
	delay := 100 * time.Millisecond * time.Duration(1<<attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
