package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"ariad/internal/visitor"

	"github.com/go-chi/chi/v5/middleware"
)

type visitorContextKey struct{}
type visitorAuthentication struct {
	service *visitor.Service
	logger  *slog.Logger
}

func (a visitorAuthentication) require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		identity, err := a.service.Authenticate(request.Context(), bearerToken(request))
		if err == nil {
			next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), visitorContextKey{}, identity)))
			return
		}
		code, message := "visitor_session_invalid", "visitor session is invalid"
		if errors.Is(err, visitor.ErrExpiredSession) {
			code, message = "visitor_session_expired", "visitor session has expired"
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("WWW-Authenticate", "Bearer")
		response.WriteHeader(http.StatusUnauthorized)
		if encodeErr := json.NewEncoder(response).Encode(map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": middleware.GetReqID(request.Context())}}); encodeErr != nil {
			a.logger.Error("write visitor authentication error", "error", encodeErr)
		}
	})
}

func visitorFromRequest(request *http.Request) visitor.Identity {
	identity, _ := request.Context().Value(visitorContextKey{}).(visitor.Identity)
	return identity
}
