package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// AdminMiddleware authenticates management requests with one configured bearer token.
type AdminMiddleware struct {
	configured bool
	tokenHash  [sha256.Size]byte
	logger     *slog.Logger
}

// NewAdminMiddleware creates reusable management-route authentication.
func NewAdminMiddleware(token string, logger *slog.Logger) *AdminMiddleware {
	middleware := &AdminMiddleware{
		configured: token != "",
		tokenHash:  sha256.Sum256([]byte(token)),
		logger:     logger,
	}
	if !middleware.configured {
		logger.Warn("admin token is not configured; management endpoints will reject all requests")
	}
	return middleware
}

// RequireAdmin rejects requests without the configured administrator bearer token.
func (m *AdminMiddleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !m.authorized(request.Header.Get("Authorization")) {
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("WWW-Authenticate", "Bearer")
			response.WriteHeader(http.StatusUnauthorized)
			if err := json.NewEncoder(response).Encode(map[string]any{
				"error": map[string]string{
					"code":    "admin_unauthorized",
					"message": "administrator authentication is required",
				},
			}); err != nil {
				m.logger.Error("write admin authentication error", "error", err)
			}
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (m *AdminMiddleware) authorized(header string) bool {
	if !m.configured {
		return false
	}
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.Contains(token, " ") {
		return false
	}
	candidateHash := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(candidateHash[:], m.tokenHash[:]) == 1
}
