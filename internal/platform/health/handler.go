// Package health exposes dependency-free process liveness checks.
package health

import (
	"encoding/json"
	"net/http"
)

type response struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// Handler reports whether the API process can serve requests. Dependency
// readiness will be exposed separately once the local platform is wired.
func Handler(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(response{Status: "ok", Service: "api"})
}
