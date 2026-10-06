package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ARIAD_API_ADDR", "")
	t.Setenv("ARIAD_LOG_LEVEL", "")
	t.Setenv("ARIAD_DATABASE_URL", "")

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
}

func TestLoadEnvironment(t *testing.T) {
	t.Setenv("ARIAD_API_ADDR", "127.0.0.1:9090")
	t.Setenv("ARIAD_LOG_LEVEL", "debug")
	t.Setenv("ARIAD_DATABASE_URL", "postgres://ariad:secret@localhost:5432/ariad")

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
}
