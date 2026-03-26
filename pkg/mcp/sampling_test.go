package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/mcp/transport"
	"github.com/ejm/go_pi/pkg/tools"
)

// testServer creates an MCPServer with the given config and manager settings for testing.
func testServer(t *testing.T, cfg *config.MCPServerConfig, mgr *MCPManager) (*MCPServer, *mockTransport) {
	t.Helper()
	mt := newMockTransport()
	s := &MCPServer{
		name:      "test",
		config:    cfg,
		transport: mt,
		manager:   mgr,
	}
	return s, mt
}

func TestHandleSamplingRequest_Disabled(t *testing.T) {
	mgr := &MCPManager{
		servers:      make(map[string]*MCPServer),
		toolRegistry: tools.NewRegistry(),
	}
	cfg := &config.MCPServerConfig{} // no sampling config
	s, mt := testServer(t, cfg, mgr)

	id := json.RawMessage(`1`)
	params, _ := json.Marshal(SamplingRequest{MaxTokens: 100})
	s.handleSamplingRequest(context.Background(), id, params)

	sent := mt.getSent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	var resp JSONRPCResponse
	json.Unmarshal(sent[0], &resp)
	if resp.Error == nil {
		t.Fatal("expected error response for disabled sampling")
	}
}

func TestHandleSamplingRequest_EnabledNoApproval(t *testing.T) {
	mgr := &MCPManager{
		servers:      make(map[string]*MCPServer),
		toolRegistry: tools.NewRegistry(),
	}
	cfg := &config.MCPServerConfig{
		Sampling: &config.SamplingConfig{Enabled: true, MaxTokens: 4096},
	}
	// No confirmSampling callback → should fail because approval is required.
	s, mt := testServer(t, cfg, mgr)

	id := json.RawMessage(`2`)
	params, _ := json.Marshal(SamplingRequest{MaxTokens: 100})
	s.handleSamplingRequest(context.Background(), id, params)

	sent := mt.getSent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	var resp JSONRPCResponse
	json.Unmarshal(sent[0], &resp)
	if resp.Error == nil {
		t.Fatal("expected error when no confirm callback and approval required")
	}
}

func TestHandleSamplingRequest_SkipApproval(t *testing.T) {
	handler := func(ctx context.Context, serverName string, req SamplingRequest) (*SamplingResponse, error) {
		return &SamplingResponse{
			Role:    "assistant",
			Content: MCPContentItem{Type: "text", Text: "hello"},
			Model:   "test-model",
		}, nil
	}
	mgr := &MCPManager{
		servers:         make(map[string]*MCPServer),
		toolRegistry:    tools.NewRegistry(),
		samplingHandler: handler,
	}
	cfg := &config.MCPServerConfig{
		Sampling: &config.SamplingConfig{
			Enabled:      true,
			MaxTokens:    4096,
			SkipApproval: true,
		},
	}
	s, mt := testServer(t, cfg, mgr)

	id := json.RawMessage(`3`)
	params, _ := json.Marshal(SamplingRequest{MaxTokens: 100})
	s.handleSamplingRequest(context.Background(), id, params)

	sent := mt.getSent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	var resp JSONRPCResponse
	json.Unmarshal(sent[0], &resp)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	var sampResp SamplingResponse
	json.Unmarshal(resp.Result, &sampResp)
	if sampResp.Content.Text != "hello" {
		t.Errorf("response text = %q, want %q", sampResp.Content.Text, "hello")
	}
}

func TestHandleSamplingRequest_MaxTokensCapped(t *testing.T) {
	var capturedReq SamplingRequest
	handler := func(ctx context.Context, serverName string, req SamplingRequest) (*SamplingResponse, error) {
		capturedReq = req
		return &SamplingResponse{
			Role:    "assistant",
			Content: MCPContentItem{Type: "text", Text: "ok"},
			Model:   "test",
		}, nil
	}
	mgr := &MCPManager{
		servers:         make(map[string]*MCPServer),
		toolRegistry:    tools.NewRegistry(),
		samplingHandler: handler,
	}
	cfg := &config.MCPServerConfig{
		Sampling: &config.SamplingConfig{
			Enabled:      true,
			MaxTokens:    100,
			SkipApproval: true,
		},
	}
	s, _ := testServer(t, cfg, mgr)

	id := json.RawMessage(`4`)
	params, _ := json.Marshal(SamplingRequest{MaxTokens: 5000})
	s.handleSamplingRequest(context.Background(), id, params)

	if capturedReq.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want 100 (capped)", capturedReq.MaxTokens)
	}
}

func TestHandleSamplingRequest_ApprovalDenied(t *testing.T) {
	mgr := &MCPManager{
		servers:      make(map[string]*MCPServer),
		toolRegistry: tools.NewRegistry(),
		confirmSampling: func(serverName string, req SamplingRequest) (bool, error) {
			return false, nil
		},
	}
	cfg := &config.MCPServerConfig{
		Sampling: &config.SamplingConfig{Enabled: true, MaxTokens: 4096},
	}
	s, mt := testServer(t, cfg, mgr)

	id := json.RawMessage(`5`)
	params, _ := json.Marshal(SamplingRequest{MaxTokens: 100})
	s.handleSamplingRequest(context.Background(), id, params)

	sent := mt.getSent()
	var resp JSONRPCResponse
	json.Unmarshal(sent[0], &resp)
	if resp.Error == nil {
		t.Fatal("expected error when user denies sampling")
	}
}

func TestHandleSamplingRequest_InvalidParams(t *testing.T) {
	mgr := &MCPManager{
		servers:      make(map[string]*MCPServer),
		toolRegistry: tools.NewRegistry(),
	}
	cfg := &config.MCPServerConfig{
		Sampling: &config.SamplingConfig{Enabled: true},
	}
	s, mt := testServer(t, cfg, mgr)

	id := json.RawMessage(`6`)
	s.handleSamplingRequest(context.Background(), id, json.RawMessage(`{invalid json`))

	sent := mt.getSent()
	var resp JSONRPCResponse
	json.Unmarshal(sent[0], &resp)
	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleSamplingRequest_ApprovalError(t *testing.T) {
	mgr := &MCPManager{
		servers:      make(map[string]*MCPServer),
		toolRegistry: tools.NewRegistry(),
		confirmSampling: func(serverName string, req SamplingRequest) (bool, error) {
			return false, fmt.Errorf("UI crashed")
		},
	}
	cfg := &config.MCPServerConfig{
		Sampling: &config.SamplingConfig{Enabled: true, MaxTokens: 4096},
	}
	s, mt := testServer(t, cfg, mgr)

	id := json.RawMessage(`7`)
	params, _ := json.Marshal(SamplingRequest{MaxTokens: 100})
	s.handleSamplingRequest(context.Background(), id, params)

	sent := mt.getSent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	var resp JSONRPCResponse
	json.Unmarshal(sent[0], &resp)
	if resp.Error == nil {
		t.Fatal("expected error when confirm callback fails")
	}
	// Should mention "approval failed", not "user denied".
	if !strings.Contains(resp.Error.Message, "approval failed") {
		t.Errorf("error message should mention approval failure, got %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "UI crashed") {
		t.Errorf("error message should include underlying error, got %q", resp.Error.Message)
	}
}

// Ensure mockTransport implements transport.Transport (compile-time check).
var _ transport.Transport = (*mockTransport)(nil)
