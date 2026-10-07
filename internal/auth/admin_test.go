package auth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireAdmin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		token  string
		header string
		status int
	}{
		{name: "unconfigured", status: http.StatusUnauthorized},
		{name: "missing", token: "secret", status: http.StatusUnauthorized},
		{name: "wrong", token: "secret", header: "Bearer wrong", status: http.StatusUnauthorized},
		{name: "malformed", token: "secret", header: "Bearer secret extra", status: http.StatusUnauthorized},
		{name: "valid", token: "secret", header: "Bearer secret", status: http.StatusNoContent},
		{name: "case insensitive scheme", token: "secret", header: "bearer secret", status: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			middleware := NewAdminMiddleware(test.token, slog.New(slog.DiscardHandler))
			handler := middleware.RequireAdmin(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/management", nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestUnconfiguredAdminTokenLogsWarning(t *testing.T) {
	var output bytes.Buffer
	NewAdminMiddleware("", slog.New(slog.NewTextHandler(&output, nil)))
	if !strings.Contains(output.String(), "admin token is not configured") {
		t.Fatalf("log = %q", output.String())
	}
}
