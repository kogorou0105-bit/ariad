package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"ariad/internal/auth"

	"github.com/go-chi/chi/v5/middleware"
)

type administratorContextKey struct{}
type adminAuthentication struct {
	service *auth.Service
	logger  *slog.Logger
}

func newAdminAuthentication(service *auth.Service, logger *slog.Logger) *adminAuthentication {
	return &adminAuthentication{service: service, logger: logger}
}
func (a *adminAuthentication) require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		administrator, err := a.service.Authenticate(request.Context(), bearerToken(request))
		if err != nil {
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("WWW-Authenticate", "Bearer")
			response.WriteHeader(http.StatusUnauthorized)
			if encodeErr := json.NewEncoder(response).Encode(map[string]any{"error": map[string]string{"code": "admin_unauthorized", "message": "administrator authentication is required", "request_id": middleware.GetReqID(request.Context())}}); encodeErr != nil {
				a.logger.Error("write admin authentication error", "error", encodeErr)
			}
			return
		}
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), administratorContextKey{}, administrator)))
	})
}
func administratorFromRequest(request *http.Request) auth.Administrator {
	administrator, _ := request.Context().Value(administratorContextKey{}).(auth.Administrator)
	return administrator
}
func bearerToken(request *http.Request) string {
	scheme, token, ok := strings.Cut(request.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.Contains(token, " ") {
		return ""
	}
	return token
}
