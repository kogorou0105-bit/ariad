package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

func decodeJSON(
	response http.ResponseWriter,
	request *http.Request,
	maximumBytes int64,
	destination any,
) error {
	request.Body = http.MaxBytesReader(response, request.Body, maximumBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func (h apiHandlers) writeError(
	response http.ResponseWriter,
	status int,
	code string,
	message string,
	requestID string,
) {
	h.writeJSON(response, status, errorEnvelope{Error: apiError{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}})
}

func (h apiHandlers) writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		h.logger.Error("write JSON response", "error", err)
	}
}
