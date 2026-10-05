package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ARIAD_API_ADDR", "")
	t.Setenv("ARIAD_LOG_LEVEL", "")

	settings := Load()
	if settings.APIAddress != ":8080" {
		t.Fatalf("APIAddress = %q, want :8080", settings.APIAddress)
	}
	if settings.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want info", settings.LogLevel)
	}
}

func TestLoadEnvironment(t *testing.T) {
	t.Setenv("ARIAD_API_ADDR", "127.0.0.1:9090")
	t.Setenv("ARIAD_LOG_LEVEL", "debug")

	settings := Load()
	if settings.APIAddress != "127.0.0.1:9090" {
		t.Fatalf("APIAddress = %q", settings.APIAddress)
	}
	if settings.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q", settings.LogLevel)
	}
}
