package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// SamplingRequest is the params for a sampling/createMessage request from a server.
type SamplingRequest struct {
	Messages         []SamplingMessage  `json:"messages"`
	ModelPreferences *ModelPreferences  `json:"modelPreferences,omitempty"`
	SystemPrompt     string             `json:"systemPrompt,omitempty"`
	IncludeContext   string             `json:"includeContext,omitempty"` // "none", "thisServer", "allServers"
	Temperature      *float64           `json:"temperature,omitempty"`
	MaxTokens        int                `json:"maxTokens"`
	StopSequences    []string           `json:"stopSequences,omitempty"`
	Metadata         json.RawMessage    `json:"metadata,omitempty"`
}

// SamplingMessage is a message in a sampling request.
type SamplingMessage struct {
	Role    string         `json:"role"`
	Content ContentItem `json:"content"`
}

// ModelPreferences describes the server's model preferences for sampling.
type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	CostPriority         float64     `json:"costPriority,omitempty"`
	SpeedPriority        float64     `json:"speedPriority,omitempty"`
	IntelligencePriority float64     `json:"intelligencePriority,omitempty"`
}

// ModelHint is a single model hint in sampling preferences.
type ModelHint struct {
	Name string `json:"name,omitempty"`
}

// SamplingResponse is the response to a sampling/createMessage request.
type SamplingResponse struct {
	Role    string         `json:"role"`
	Content ContentItem `json:"content"`
	Model   string         `json:"model"`
}

// SamplingHandler is the callback for handling sampling requests. Implementations
// should invoke the LLM provider and return a response. The handler is called
// after approval checks pass.
type SamplingHandler func(ctx context.Context, serverName string, req SamplingRequest) (*SamplingResponse, error)

// ConfirmSamplingFunc asks the user for sampling approval in interactive mode.
// Returns true if approved.
type ConfirmSamplingFunc func(serverName string, req SamplingRequest) (bool, error)

// handleSamplingRequest processes a sampling/createMessage request from a server.
// Security checks:
//   - Sampling must be enabled for the server
//   - If SkipApproval is false (default), requires interactive approval
//   - MaxTokens is capped to the configured limit
func (s *Server) handleSamplingRequest(ctx context.Context, id json.RawMessage, params json.RawMessage) {
	var req SamplingRequest
	if err := json.Unmarshal(params, &req); err != nil {
		s.respondError(ctx, id, ErrCodeInvalidParams, "invalid sampling params: "+err.Error())
		return
	}

	// Check if sampling is enabled.
	if s.config.Sampling == nil || !s.config.Sampling.Enabled {
		s.respondError(ctx, id, ErrCodeInvalidRequest,
			fmt.Sprintf("MCP server %q requested sampling but it is not enabled", s.name))
		return
	}

	// Check approval.
	if !s.config.Sampling.SkipApproval {
		if s.manager.confirmSampling == nil {
			s.respondError(ctx, id, ErrCodeInvalidRequest,
				fmt.Sprintf("MCP server %q requested sampling but approval is required and no interactive session is available", s.name))
			return
		}
		approved, err := s.manager.confirmSampling(s.name, req)
		if err != nil {
			s.respondError(ctx, id, ErrCodeInvalidRequest,
				fmt.Sprintf("sampling approval failed for MCP server %q: %v", s.name, err))
			return
		}
		if !approved {
			s.respondError(ctx, id, ErrCodeInvalidRequest,
				fmt.Sprintf("user denied sampling request from MCP server %q", s.name))
			return
		}
	}

	// Cap maxTokens.
	if s.config.Sampling.MaxTokens > 0 && req.MaxTokens > s.config.Sampling.MaxTokens {
		req.MaxTokens = s.config.Sampling.MaxTokens
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 1024 // reasonable default
	}

	// Delegate to the sampling handler.
	if s.manager.samplingHandler == nil {
		s.respondError(ctx, id, ErrCodeInternalError, "no sampling handler configured")
		return
	}

	resp, err := s.manager.samplingHandler(ctx, s.name, req)
	if err != nil {
		s.respondError(ctx, id, ErrCodeInternalError, "sampling failed: "+err.Error())
		return
	}

	s.respondResult(ctx, id, resp)
}

// respondResult sends a JSON-RPC success response.
func (s *Server) respondResult(ctx context.Context, id json.RawMessage, result any) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		log.Printf("mcp: server %q: failed to marshal result for id %s: %v", s.name, string(id), err)
		return
	}
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  resultJSON,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("mcp: server %q: failed to marshal response for id %s: %v", s.name, string(id), err)
		return
	}
	_ = s.transport.Send(ctx, data)
}

// respondError sends a JSON-RPC error response.
func (s *Server) respondError(ctx context.Context, id json.RawMessage, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("mcp: server %q: failed to marshal error response for id %s: %v", s.name, string(id), err)
		return
	}
	_ = s.transport.Send(ctx, data)
}
