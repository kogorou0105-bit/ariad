package main

import (
	"errors"
	"net/http"
	"time"

	"ariad/internal/auth"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type administratorCredentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type administratorResponse struct {
	AdministratorID string `json:"administrator_id"`
	Username        string `json:"username"`
}
type loginResponse struct {
	Token         string                `json:"token"`
	ExpiresAt     time.Time             `json:"expires_at"`
	Administrator administratorResponse `json:"administrator"`
}
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h apiHandlers) loginAdministrator(response http.ResponseWriter, request *http.Request) {
	var input administratorCredentialsRequest
	if err := decodeJSON(response, request, maximumAuthBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	token, administrator, expiresAt, err := h.auth.Login(request.Context(), input.Username, input.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		h.writeError(response, http.StatusUnauthorized, "invalid_credentials", "invalid username or password", middleware.GetReqID(request.Context()))
		return
	}
	if errors.Is(err, auth.ErrTooManyAttempts) {
		h.writeError(response, http.StatusTooManyRequests, "login_rate_limited", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not create administrator session", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, loginResponse{Token: token, ExpiresAt: expiresAt, Administrator: administratorResponse{AdministratorID: administrator.ID, Username: administrator.Username}})
}

func (h apiHandlers) listAdministrators(response http.ResponseWriter, request *http.Request) {
	administrators, err := h.auth.ListAdministrators(request.Context())
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not list administrators", middleware.GetReqID(request.Context()))
		return
	}
	items := make([]administratorResponse, 0, len(administrators))
	for _, administrator := range administrators {
		items = append(items, administratorResponse{AdministratorID: administrator.ID, Username: administrator.Username})
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"administrators": items})
}

func (h apiHandlers) changeAdministratorPassword(response http.ResponseWriter, request *http.Request) {
	var input changePasswordRequest
	if err := decodeJSON(response, request, maximumAuthBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	administrator := authenticatedAdministrator(request)
	err := h.auth.ChangePassword(request.Context(), administrator.ID, input.CurrentPassword, input.NewPassword)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		h.writeError(response, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect", middleware.GetReqID(request.Context()))
		return
	}
	if errors.Is(err, auth.ErrPasswordTooShort) {
		h.writeError(response, http.StatusBadRequest, "invalid_password", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not change password", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]bool{"password_changed": true})
}

func (h apiHandlers) deleteAdministrator(response http.ResponseWriter, request *http.Request) {
	current := authenticatedAdministrator(request)
	err := h.auth.DeleteAdministrator(request.Context(), current.ID, chi.URLParam(request, "administrator_id"))
	if errors.Is(err, auth.ErrAdministratorNotFound) {
		h.writeError(response, http.StatusNotFound, "administrator_not_found", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if errors.Is(err, auth.ErrCannotDeleteSelf) || errors.Is(err, auth.ErrLastAdministrator) {
		h.writeError(response, http.StatusConflict, "administrator_not_deletable", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not delete administrator", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]bool{"deleted": true})
}

func (h apiHandlers) logoutAdministrator(response http.ResponseWriter, request *http.Request) {
	if err := h.auth.Logout(request.Context(), bearerToken(request)); err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not end administrator session", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]bool{"logged_out": true})
}

func (h apiHandlers) createAdministrator(response http.ResponseWriter, request *http.Request) {
	var input administratorCredentialsRequest
	if err := decodeJSON(response, request, maximumAuthBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	administrator, err := h.auth.CreateAdministrator(request.Context(), input.Username, input.Password)
	if errors.Is(err, auth.ErrUsernameRequired) || errors.Is(err, auth.ErrPasswordTooShort) {
		h.writeError(response, http.StatusBadRequest, "invalid_administrator", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if errors.Is(err, auth.ErrUsernameExists) {
		h.writeError(response, http.StatusConflict, "administrator_exists", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not create administrator", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusCreated, administratorResponse{AdministratorID: administrator.ID, Username: administrator.Username})
}

func authenticatedAdministrator(request *http.Request) auth.Administrator {
	return administratorFromRequest(request)
}
