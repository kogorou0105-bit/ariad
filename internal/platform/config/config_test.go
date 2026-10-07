package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ARIAD_API_ADDR", "")
	t.Setenv("ARIAD_LOG_LEVEL", "")
	t.Setenv("ARIAD_DATABASE_URL", "")
	t.Setenv("ARIAD_ADMIN_TOKEN", "")
	t.Setenv("ARIAD_MODEL_CONFIG_ENCRYPTION_KEY", "")
	t.Setenv("ARIAD_CONVERSATION_HISTORY_TURN_LIMIT", "")

	settings := Load()
	if settings.APIAddress != ":8080" {
		t.Fatalf("APIAddress = %q, want :8080", settings.APIAddress)
	}
	if settings.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want info", settings.LogLevel)
	}
	if settings.DatabaseURL != "" {
		t.Fatalf("DatabaseURL = %q, want empty", settings.DatabaseURL)
	}
	if settings.AdminToken != "" {
		t.Fatalf("AdminToken = %q, want empty", settings.AdminToken)
	}
	if settings.ModelConfigEncryptionKey != "" {
		t.Fatalf("ModelConfigEncryptionKey must be empty")
	}
	if settings.ConversationHistoryTurnLimit != 5 {
		t.Fatalf("ConversationHistoryTurnLimit = %d, want 5", settings.ConversationHistoryTurnLimit)
	}
}

func TestLoadEnvironment(t *testing.T) {
	t.Setenv("ARIAD_API_ADDR", "127.0.0.1:9090")
	t.Setenv("ARIAD_LOG_LEVEL", "debug")
	t.Setenv("ARIAD_DATABASE_URL", "postgres://ariad:secret@localhost:5432/ariad")
	t.Setenv("ARIAD_ADMIN_TOKEN", "admin-secret")
	t.Setenv("ARIAD_MODEL_CONFIG_ENCRYPTION_KEY", "encoded-key")
	t.Setenv("ARIAD_CONVERSATION_HISTORY_TURN_LIMIT", "8")

	settings := Load()
	if settings.APIAddress != "127.0.0.1:9090" {
		t.Fatalf("APIAddress = %q", settings.APIAddress)
	}
	if settings.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q", settings.LogLevel)
	}
	if settings.DatabaseURL != "postgres://ariad:secret@localhost:5432/ariad" {
		t.Fatalf("DatabaseURL = %q", settings.DatabaseURL)
	}
	if settings.AdminToken != "admin-secret" {
		t.Fatalf("AdminToken = %q", settings.AdminToken)
	}
	if settings.ModelConfigEncryptionKey != "encoded-key" {
		t.Fatalf("ModelConfigEncryptionKey = %q", settings.ModelConfigEncryptionKey)
	}
	if settings.ConversationHistoryTurnLimit != 8 {
		t.Fatalf("ConversationHistoryTurnLimit = %d", settings.ConversationHistoryTurnLimit)
	}
}

func TestLoadFallsBackForInvalidHistoryLimit(t *testing.T) {
	t.Setenv("ARIAD_CONVERSATION_HISTORY_TURN_LIMIT", "0")
	if got := Load().ConversationHistoryTurnLimit; got != 5 {
		t.Fatalf("ConversationHistoryTurnLimit = %d, want 5", got)
	}
}
