// Package model contains provider adapters for the runtime model port.
package model

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"ariad/internal/runtime"
)

// Stub is a deterministic, evidence-only local model adapter.
type Stub struct{}

var _ runtime.Model = (*Stub)(nil)

// NewStub creates a deterministic model that requires no API key.
func NewStub() *Stub {
	return &Stub{}
}

// Generate answers with the highest-ranked evidence and cites only its ID.
func (*Stub) Generate(
	ctx context.Context,
	request runtime.ModelRequest,
) (runtime.ModelResponse, error) {
	if err := ctx.Err(); err != nil {
		return runtime.ModelResponse{}, fmt.Errorf("stub model: %w", err)
	}
	if len(request.Evidence) == 0 {
		return runtime.ModelResponse{}, nil
	}

	selected := request.Evidence[0]
	answer := "According to the provided knowledge: " + selected.Text
	if strings.HasPrefix(strings.ToLower(request.Locale), "zh") {
		answer = "根据已提供的知识：" + selected.Text
	}
	input := request.Question + request.Instructions
	for _, turn := range request.History {
		input += turn.Question + turn.Answer
	}
	for _, item := range request.Evidence {
		input += item.Text
	}

	return runtime.ModelResponse{
		Text:             answer,
		CitedEvidenceIDs: []string{selected.EvidenceID},
		Usage: runtime.ModelUsage{
			Invoked:     true,
			Provider:    "stub",
			Model:       "grounded-v1",
			InputUnits:  int64(utf8.RuneCountInString(input)),
			OutputUnits: int64(utf8.RuneCountInString(answer)),
		},
	}, nil
}
