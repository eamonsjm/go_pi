package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/skill"
	"github.com/ejm/go_pi/pkg/tools"
)

// --- Test helpers ---

// autoResponder wraps a mockTransport and auto-responds to JSON-RPC requests
// using registered handlers. This avoids the timing issues of polling-based
// approaches by intercepting sends inline.
type autoResponder struct {
	*mockTransport

	mu       sync.Mutex
	handlers map[string]func(id json.RawMessage, params json.RawMessage)
}

func newAutoResponder() *autoResponder {
	return &autoResponder{
		mockTransport: newMockTransport(),
		handlers:      make(map[string]func(json.RawMessage, json.RawMessage)),
	}
}

// Send intercepts outgoing messages. If a handler exists for the method,
// it is called asynchronously to feed a response back into incoming.
func (a *autoResponder) Send(ctx context.Context, msg json.RawMessage) error {
	if err := a.mockTransport.Send(ctx, msg); err != nil {
		return err
	}

	// Parse to see if we should auto-respond.
	var req JSONRPCRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return nil
	}

	// Notifications (no id) don't need responses.
	if req.ID == nil {
		return nil
	}

	a.mu.Lock()
	h, ok := a.handlers[req.Method]
	a.mu.Unlock()

	if ok {
		// Respond asynchronously to avoid deadlocking the client.
		go h(req.ID, req.Params)
	}

	return nil
}

func (a *autoResponder) setHandler(method string, h func(id json.RawMessage, params json.RawMessage)) {
	a.mu.Lock()
	a.handlers[method] = h
	a.mu.Unlock()
}

// respond sends a JSON-RPC response into the transport's incoming channel.
func (a *autoResponder) respond(id json.RawMessage, result any) {
	resultJSON, _ := json.Marshal(result)
	resp := JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: resultJSON}
	data, _ := json.Marshal(resp)
	a.incoming <- data
}

// respondError sends a JSON-RPC error response.
func (a *autoResponder) respondError(id json.RawMessage, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	}
	data, _ := json.Marshal(resp)
	a.incoming <- data
}

// setupInitialize registers an auto-handler for the initialize method.
func (a *autoResponder) setupInitialize(caps ServerCapabilities, instructions string) {
	a.setHandler("initialize", func(id json.RawMessage, _ json.RawMessage) {
		a.respond(id, InitializeResult{
			ProtocolVersion: "2025-11-25",
			Capabilities:    caps,
			ServerInfo:      ImplementationInfo{Name: "test-server", Version: "1.0"},
			Instructions:    instructions,
		})
	})
}

// setupToolsList registers an auto-handler for tools/list.
func (a *autoResponder) setupToolsList(toolInfos []ToolInfo) {
	a.setHandler("tools/list", func(id json.RawMessage, _ json.RawMessage) {
		a.respond(id, ToolsListPage{Tools: toolInfos})
	})
}

// setupPromptsList registers an auto-handler for prompts/list.
func (a *autoResponder) setupPromptsList(prompts []PromptInfo) {
	a.setHandler("prompts/list", func(id json.RawMessage, _ json.RawMessage) {
		a.respond(id, PromptsListPage{Prompts: prompts})
	})
}

// setupResourcesList registers an auto-handler for resources/list.
func (a *autoResponder) setupResourcesList(resources []ResourceInfo) {
	a.setHandler("resources/list", func(id json.RawMessage, _ json.RawMessage) {
		a.respond(id, ResourcesListPage{Resources: resources})
	})
}

// setupToolsCall registers an auto-handler for tools/call.
func (a *autoResponder) setupToolsCall(resultText string, isError bool) {
	a.setHandler("tools/call", func(id json.RawMessage, _ json.RawMessage) {
		a.respond(id, ToolResult{
			Content: []ContentItem{{Type: "text", Text: resultText}},
			IsError: isError,
		})
	})
}

// buildTestServer creates a Manager with a single auto-responding mock
// server. The server is initialized and registered in the manager.
func buildTestServer(t *testing.T, name string, cfg *config.MCPServerConfig, ar *autoResponder) (*Manager, *Server) {
	t.Helper()

	reg := tools.NewRegistry()
	skillReg := skill.NewRegistry()

	mgr := NewManager(ManagerConfig{
		ToolRegistry:  reg,
		SkillRegistry: skillReg,
		WorkingDir:    "/test/project",
		ConfigDir:     "/test/.gi",
		ProjectPath:   "/test/project",
		ClientName:    "gi-test",
		ClientVersion: "0.0.1",
	})

	server := newServer(name, cfg, ar, mgr)

	if err := server.initialize(context.Background()); err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	mgr.mu.Lock()
	mgr.servers[name] = server
	mgr.serverList = append(mgr.serverList, name)
	mgr.mu.Unlock()

	return mgr, server
}

// --- Integration Tests ---

func TestIntegration_ServerLifecycle(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{Tools: &ToolCapability{ListChanged: true}}, "")

	mgr, _ := buildTestServer(t, "lifecycle", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	// Verify server is tracked.
	names := mgr.ServerNames()
	if len(names) != 1 || names[0] != "lifecycle" {
		t.Errorf("ServerNames = %v, want [lifecycle]", names)
	}

	server := mgr.Server("lifecycle")
	if server == nil {
		t.Fatal("Server(lifecycle) returned nil")
	}

	if v := server.client.NegotiatedVersion(); v != "2025-11-25" {
		t.Errorf("NegotiatedVersion = %q, want 2025-11-25", v)
	}

	// Shutdown should clear everything.
	mgr.Shutdown(context.Background())
	if s := mgr.Server("lifecycle"); s != nil {
		t.Error("Server should be nil after Shutdown")
	}
	if names := mgr.ServerNames(); len(names) != 0 {
		t.Errorf("ServerNames after Shutdown = %v", names)
	}
}

func TestIntegration_ToolBridging(t *testing.T) {
	ar := newAutoResponder()
	boolTrue := true
	ar.setupInitialize(ServerCapabilities{Tools: &ToolCapability{ListChanged: true}}, "")
	ar.setupToolsList([]ToolInfo{
		{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
			Annotations: &ToolAnnotations{ReadOnlyHint: &boolTrue},
		},
		{
			Name:        "delete_file",
			Description: "Delete a file",
			InputSchema: map[string]any{"type": "object"},
		},
	})

	mgr, server := buildTestServer(t, "fs", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	// Discover tools.
	if err := server.discoverAndRegisterTools(context.Background()); err != nil {
		t.Fatalf("discoverAndRegisterTools: %v", err)
	}

	// Verify tools registered in the registry.
	readTool, ok := mgr.toolRegistry.Get("mcp__fs__read_file")
	if !ok {
		t.Fatal("mcp__fs__read_file not found in registry")
	}
	if readTool.Description() != "Read a file" {
		t.Errorf("description = %q, want %q", readTool.Description(), "Read a file")
	}

	deleteTool, ok := mgr.toolRegistry.Get("mcp__fs__delete_file")
	if !ok {
		t.Fatal("mcp__fs__delete_file not found in registry")
	}
	if deleteTool.Description() != "Delete a file" {
		t.Errorf("description = %q", deleteTool.Description())
	}

	// Verify annotations.
	annot := mgr.GetAnnotations("fs", "read_file")
	if annot == nil {
		t.Fatal("annotations for read_file should not be nil")
	}
	if !AnnotationReadOnly(annot) {
		t.Error("read_file should have readOnlyHint=true")
	}
}

func TestIntegration_RichToolError(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{Tools: &ToolCapability{}}, "")
	ar.setupToolsList([]ToolInfo{
		{Name: "fail_tool", Description: "Always fails", InputSchema: map[string]any{"type": "object"}},
	})
	ar.setupToolsCall("permission denied: /etc/shadow", true)

	mgr, server := buildTestServer(t, "err", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	if err := server.discoverAndRegisterTools(context.Background()); err != nil {
		t.Fatalf("discoverAndRegisterTools: %v", err)
	}

	tool, ok := mgr.toolRegistry.Get("mcp__err__fail_tool")
	if !ok {
		t.Fatal("fail_tool not found")
	}

	richTool, ok := tool.(tools.RichTool)
	if !ok {
		t.Fatal("Tool should implement RichTool")
	}

	// Execute — the mock returns isError=true.
	_, err := richTool.ExecuteRich(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected RichToolError, got nil")
	}

	// The error message should contain the text from the server.
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error should contain 'permission denied', got: %v", err)
	}
}

func TestIntegration_ConcurrentToolCalls(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{Tools: &ToolCapability{}}, "")
	ar.setupToolsList([]ToolInfo{
		{Name: "echo", Description: "Echo tool", InputSchema: map[string]any{"type": "object"}},
	})

	var callCount int64
	var callMu sync.Mutex
	ar.setHandler("tools/call", func(id json.RawMessage, _ json.RawMessage) {
		callMu.Lock()
		callCount++
		n := callCount
		callMu.Unlock()
		// Small delay to simulate real server work.
		time.Sleep(10 * time.Millisecond)
		ar.respond(id, ToolResult{
			Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("result-%d", n)}},
		})
	})

	mgr, server := buildTestServer(t, "concurrent", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	if err := server.discoverAndRegisterTools(context.Background()); err != nil {
		t.Fatalf("discoverAndRegisterTools: %v", err)
	}

	tool, _ := mgr.toolRegistry.Get("mcp__concurrent__echo")
	richTool := tool.(tools.RichTool)

	// Launch 5 concurrent calls.
	const numCalls = 5
	var wg sync.WaitGroup
	errors := make([]error, numCalls)
	results := make([]string, numCalls)

	for i := 0; i < numCalls; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			blocks, err := richTool.ExecuteRich(context.Background(), map[string]any{})
			errors[i] = err
			if err == nil && len(blocks) > 0 {
				results[i] = blocks[0].Text
			}
		}()
	}

	wg.Wait()

	for i, err := range errors {
		if err != nil {
			t.Errorf("call %d: unexpected error: %v", i, err)
		}
	}

	callMu.Lock()
	if callCount != numCalls {
		t.Errorf("expected %d calls, got %d", numCalls, callCount)
	}
	callMu.Unlock()
}

func TestIntegration_ListChangedRediscovery(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{Tools: &ToolCapability{ListChanged: true}}, "")

	// Initial tool list.
	ar.setupToolsList([]ToolInfo{
		{Name: "tool_a", Description: "Tool A", InputSchema: map[string]any{"type": "object"}},
	})

	mgr, server := buildTestServer(t, "rediscover", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	if err := server.discoverAndRegisterTools(context.Background()); err != nil {
		t.Fatalf("discoverAndRegisterTools: %v", err)
	}

	if _, ok := mgr.toolRegistry.Get("mcp__rediscover__tool_a"); !ok {
		t.Fatal("tool_a not found after initial discovery")
	}

	// Update tool list for re-discovery.
	ar.setupToolsList([]ToolInfo{
		{Name: "tool_a", Description: "Tool A v2", InputSchema: map[string]any{"type": "object"}},
		{Name: "tool_b", Description: "Tool B", InputSchema: map[string]any{"type": "object"}},
	})

	// Simulate tools/list_changed notification triggering re-discovery.
	server.handleToolsListChanged()

	// Verify updated tools.
	toolA, ok := mgr.toolRegistry.Get("mcp__rediscover__tool_a")
	if !ok {
		t.Fatal("tool_a should still exist after re-discovery")
	}
	if toolA.Description() != "Tool A v2" {
		t.Errorf("tool_a description = %q, want %q", toolA.Description(), "Tool A v2")
	}

	if _, ok := mgr.toolRegistry.Get("mcp__rediscover__tool_b"); !ok {
		t.Fatal("tool_b should exist after re-discovery")
	}

	msgs := mgr.DrainSystemMessages()
	if len(msgs) == 0 {
		t.Fatal("expected a system message about tool update")
	}
	if !strings.Contains(msgs[0], "tools updated") {
		t.Errorf("system message = %q", msgs[0])
	}
}

func TestIntegration_PermissionHook(t *testing.T) {
	reg := tools.NewRegistry()
	boolTrue := true
	reg.Register(&Tool{
		name:         "mcp__fs__read_file",
		originalName: "read_file",
		desc:         "Read",
		inputSchema:  map[string]any{"type": "object"},
		annotations:  &ToolAnnotations{ReadOnlyHint: &boolTrue},
	})
	reg.Register(&Tool{
		name:         "mcp__fs__delete_file",
		originalName: "delete_file",
		desc:         "Delete",
		inputSchema:  map[string]any{"type": "object"},
	})

	permConfigs := map[string]*config.MCPPermissionConfig{
		"fs": {
			AutoApprove: []string{"read_file"},
			Deny:        []string{"delete_file"},
		},
	}

	hook := NewPermissionHook(permConfigs, func(server, tool, desc string) (bool, error) {
		return false, nil
	})
	mgr := NewManager(ManagerConfig{ToolRegistry: reg})
	hook.SetAnnotationSource(NewAnnotationLookup(mgr.GetAnnotations))

	ctx := context.Background()

	// Auto-approved tool should pass.
	if err := hook.BeforeExecute(ctx, "mcp__fs__read_file", nil); err != nil {
		t.Errorf("read_file should be auto-approved: %v", err)
	}

	// Denied tool should fail.
	if err := hook.BeforeExecute(ctx, "mcp__fs__delete_file", nil); err == nil {
		t.Error("delete_file should be denied")
	} else if !strings.Contains(err.Error(), "denied by configuration") {
		t.Errorf("unexpected deny error: %v", err)
	}

	// Non-MCP tool should pass.
	if err := hook.BeforeExecute(ctx, "bash", nil); err != nil {
		t.Errorf("non-MCP tool should pass: %v", err)
	}
}

func TestIntegration_PermissionHookAnnotationBypass(t *testing.T) {
	reg := tools.NewRegistry()
	boolTrue := true
	reg.Register(&Tool{
		name:         "mcp__db__query",
		originalName: "query",
		desc:         "Query",
		inputSchema:  map[string]any{"type": "object"},
		annotations:  &ToolAnnotations{ReadOnlyHint: &boolTrue},
	})

	hook := NewPermissionHook(nil, func(server, tool, desc string) (bool, error) {
		t.Error("confirm should not be called for read-only tool")
		return false, nil
	})
	mgr := NewManager(ManagerConfig{ToolRegistry: reg})
	hook.SetAnnotationSource(NewAnnotationLookup(mgr.GetAnnotations))

	if err := hook.BeforeExecute(context.Background(), "mcp__db__query", nil); err != nil {
		t.Errorf("read-only tool should bypass confirmation: %v", err)
	}
}

func TestIntegration_PermissionHookNonInteractive(t *testing.T) {
	hook := NewPermissionHook(nil, nil)
	err := hook.BeforeExecute(context.Background(), "mcp__srv__dangerous_tool", nil)
	if err == nil {
		t.Error("destructive tool should be denied in non-interactive mode")
	}
	if !strings.Contains(err.Error(), "requires confirmation") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestIntegration_PermissionDenyOverridesAutoApprove(t *testing.T) {
	permConfigs := map[string]*config.MCPPermissionConfig{
		"srv": {
			AutoApprove: []string{"dangerous"},
			Deny:        []string{"dangerous"},
		},
	}
	hook := NewPermissionHook(permConfigs, func(server, tool, desc string) (bool, error) {
		t.Error("confirm should not be called for denied tool")
		return true, nil
	})

	err := hook.BeforeExecute(context.Background(), "mcp__srv__dangerous", nil)
	if err == nil {
		t.Error("tool in both auto_approve and deny should be denied")
	}
	if !strings.Contains(err.Error(), "denied by configuration") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestIntegration_SamplingApproval(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{}, "")

	cfg := &config.MCPServerConfig{
		Sampling: &config.SamplingConfig{Enabled: true, MaxTokens: 500},
	}

	reg := tools.NewRegistry()
	skillReg := skill.NewRegistry()

	var samplingCalled bool
	var samplingMaxTokens int
	mgr := NewManager(ManagerConfig{
		ToolRegistry:  reg,
		SkillRegistry: skillReg,
		WorkingDir:    "/test",
		ClientName:    "gi-test",
		ClientVersion: "0.0.1",
		SamplingHandler: func(_ context.Context, serverName string, req SamplingRequest) (*SamplingResponse, error) {
			samplingCalled = true
			samplingMaxTokens = req.MaxTokens
			return &SamplingResponse{
				Role:    "assistant",
				Content: ContentItem{Type: "text", Text: "sampled"},
				Model:   "test-model",
			}, nil
		},
		ConfirmSampling: func(serverName string, req SamplingRequest) (bool, error) {
			return true, nil // approve
		},
	})

	server := newServer("samp", cfg, ar, mgr)
	if err := server.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	mgr.mu.Lock()
	mgr.servers["samp"] = server
	mgr.serverList = append(mgr.serverList, "samp")
	mgr.mu.Unlock()
	defer mgr.Shutdown(context.Background())

	// Simulate a sampling request with maxTokens > cap.
	sampReq := SamplingRequest{
		Messages:  []SamplingMessage{{Role: "user", Content: ContentItem{Type: "text", Text: "hello"}}},
		MaxTokens: 1000,
	}
	sampReqJSON, _ := json.Marshal(sampReq)
	server.handleSamplingRequest(context.Background(), json.RawMessage(`99`), sampReqJSON)

	if !samplingCalled {
		t.Error("sampling handler should have been called")
	}
	if samplingMaxTokens != 500 {
		t.Errorf("maxTokens = %d, want 500 (capped)", samplingMaxTokens)
	}
}

func TestIntegration_SamplingDenied(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{}, "")

	cfg := &config.MCPServerConfig{
		Sampling: &config.SamplingConfig{Enabled: true, MaxTokens: 500},
	}

	mgr := NewManager(ManagerConfig{
		ToolRegistry: tools.NewRegistry(),
		ClientName:   "gi-test",
		ConfirmSampling: func(serverName string, req SamplingRequest) (bool, error) {
			return false, nil
		},
	})

	server := newServer("samp", cfg, ar, mgr)
	if err := server.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	mgr.mu.Lock()
	mgr.servers["samp"] = server
	mgr.mu.Unlock()
	defer mgr.Shutdown(context.Background())

	sampReq := SamplingRequest{
		Messages:  []SamplingMessage{{Role: "user", Content: ContentItem{Type: "text", Text: "hello"}}},
		MaxTokens: 100,
	}
	sampReqJSON, _ := json.Marshal(sampReq)
	server.handleSamplingRequest(context.Background(), json.RawMessage(`100`), sampReqJSON)

	// The error response should have been sent via the transport.
	sent := ar.getSent()
	foundDeny := false
	for _, raw := range sent {
		var resp JSONRPCResponse
		if json.Unmarshal(raw, &resp) == nil && resp.Error != nil {
			if strings.Contains(resp.Error.Message, "denied") {
				foundDeny = true
			}
		}
	}
	if !foundDeny {
		t.Error("expected error response for denied sampling")
	}
}

func TestIntegration_SamplingDisabled(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{}, "")

	cfg := &config.MCPServerConfig{} // no sampling config
	mgr := NewManager(ManagerConfig{ToolRegistry: tools.NewRegistry(), ClientName: "gi-test"})

	server := newServer("nosamp", cfg, ar, mgr)
	if err := server.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	mgr.mu.Lock()
	mgr.servers["nosamp"] = server
	mgr.mu.Unlock()
	defer mgr.Shutdown(context.Background())

	sampReq := SamplingRequest{Messages: []SamplingMessage{{Role: "user", Content: ContentItem{Type: "text", Text: "hello"}}}}
	sampReqJSON, _ := json.Marshal(sampReq)
	server.handleSamplingRequest(context.Background(), json.RawMessage(`101`), sampReqJSON)

	sent := ar.getSent()
	foundErr := false
	for _, raw := range sent {
		var resp JSONRPCResponse
		if json.Unmarshal(raw, &resp) == nil && resp.Error != nil {
			if strings.Contains(resp.Error.Message, "not enabled") {
				foundErr = true
			}
		}
	}
	if !foundErr {
		t.Error("expected error response for disabled sampling")
	}
}

func TestIntegration_VersionNegotiation(t *testing.T) {
	ar := newAutoResponder()
	// Server responds with an older supported version.
	ar.setHandler("initialize", func(id json.RawMessage, _ json.RawMessage) {
		ar.respond(id, InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities:    ServerCapabilities{},
			ServerInfo:      ImplementationInfo{Name: "old-server"},
		})
	})

	reg := tools.NewRegistry()
	mgr := NewManager(ManagerConfig{ToolRegistry: reg, ClientName: "gi-test", ClientVersion: "0.0.1"})

	server := newServer("oldserver", &config.MCPServerConfig{}, ar, mgr)
	if err := server.initialize(context.Background()); err != nil {
		t.Fatalf("initialize with older version should succeed: %v", err)
	}

	if v := server.client.NegotiatedVersion(); v != "2024-11-05" {
		t.Errorf("negotiated version = %q, want 2024-11-05", v)
	}

	mgr.mu.Lock()
	mgr.servers["oldserver"] = server
	mgr.mu.Unlock()
	mgr.Shutdown(context.Background())
}

func TestIntegration_VersionNegotiationUnsupported(t *testing.T) {
	ar := newAutoResponder()
	ar.setHandler("initialize", func(id json.RawMessage, _ json.RawMessage) {
		ar.respond(id, InitializeResult{
			ProtocolVersion: "1999-01-01",
			Capabilities:    ServerCapabilities{},
			ServerInfo:      ImplementationInfo{Name: "ancient-server"},
		})
	})

	reg := tools.NewRegistry()
	mgr := NewManager(ManagerConfig{ToolRegistry: reg, ClientName: "gi-test", ClientVersion: "0.0.1"})

	server := newServer("badver", &config.MCPServerConfig{}, ar, mgr)
	err := server.initialize(context.Background())
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
	if !strings.Contains(err.Error(), "unsupported server protocol version") {
		t.Errorf("unexpected error: %v", err)
	}

	ar.Close()
}

func TestIntegration_MalformedJSONRPC(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{Tools: &ToolCapability{}}, "")

	mgr, _ := buildTestServer(t, "malformed", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	// Send malformed messages. The demux should log and skip them.
	ar.incoming <- json.RawMessage(`not json at all`)
	ar.incoming <- json.RawMessage(`{"jsonrpc":"2.0"}`) // no id, no method
	ar.incoming <- json.RawMessage(`{"jsonrpc":"2.0","id":"999"}`) // unknown id

	// Give the demux time to process.
	time.Sleep(100 * time.Millisecond)

	// Server should still be operational — send a valid notification.
	notif := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/message",
		Params:  json.RawMessage(`{"level":"info","data":"still alive"}`),
	}
	data, _ := json.Marshal(notif)
	ar.incoming <- data

	time.Sleep(50 * time.Millisecond)
	// No crash = success.
}

func TestIntegration_PaginationLimits(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{Tools: &ToolCapability{}}, "")

	pageCount := 0
	var pageMu sync.Mutex
	ar.setHandler("tools/list", func(id json.RawMessage, _ json.RawMessage) {
		pageMu.Lock()
		pageCount++
		n := pageCount
		pageMu.Unlock()

		page := ToolsListPage{
			Tools: []ToolInfo{
				{Name: fmt.Sprintf("tool_%d", n), Description: "paginated", InputSchema: map[string]any{"type": "object"}},
			},
		}
		if n < 3 {
			page.NextCursor = fmt.Sprintf("cursor_%d", n)
		}
		ar.respond(id, page)
	})

	mgr, server := buildTestServer(t, "paginated", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	if err := server.discoverAndRegisterTools(context.Background()); err != nil {
		t.Fatalf("paginated discovery: %v", err)
	}

	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("mcp__paginated__tool_%d", i)
		if _, ok := mgr.toolRegistry.Get(name); !ok {
			t.Errorf("tool %q not found", name)
		}
	}
}

func TestIntegration_ToolCallTimeout(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{Tools: &ToolCapability{}}, "")
	ar.setupToolsList([]ToolInfo{
		{Name: "slow", Description: "Slow tool", InputSchema: map[string]any{"type": "object"}},
	})
	// Do NOT set up tools/call handler — let it hang.

	mgr, server := buildTestServer(t, "timeout", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	if err := server.discoverAndRegisterTools(context.Background()); err != nil {
		t.Fatalf("discoverAndRegisterTools: %v", err)
	}

	tool, _ := mgr.toolRegistry.Get("mcp__timeout__slow")
	richTool := tool.(tools.RichTool)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := richTool.ExecuteRich(ctx, map[string]any{})
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestIntegration_DrainSystemMessages(t *testing.T) {
	mgr := NewManager(ManagerConfig{ToolRegistry: tools.NewRegistry()})

	mgr.injectSystemMessage("[MCP server fs tools updated — 1 added, 0 removed, 3 total]")
	mgr.injectSystemMessage("[MCP server db restarted]")

	msgs := mgr.DrainSystemMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "tools updated") {
		t.Errorf("msg[0] = %q", msgs[0])
	}

	msgs = mgr.DrainSystemMessages()
	if len(msgs) != 0 {
		t.Errorf("expected 0 after drain, got %d", len(msgs))
	}
}

func TestIntegration_ServerInstructionsInjection(t *testing.T) {
	mgr := NewManager(ManagerConfig{ToolRegistry: tools.NewRegistry()})

	mgr.mu.Lock()
	mgr.servers["helper"] = &Server{
		name:         "helper",
		config:       &config.MCPServerConfig{},
		instructions: "Use this server for database queries.",
	}
	mgr.servers["ignored"] = &Server{
		name:         "ignored",
		config:       &config.MCPServerConfig{Instructions: "ignore"},
		instructions: "Should not appear.",
	}
	mgr.serverList = []string{"helper", "ignored"}
	mgr.mu.Unlock()

	result := mgr.ServerInstructions()
	if !strings.Contains(result, "database queries") {
		t.Error("instructions should include helper server instructions")
	}
	if strings.Contains(result, "Should not appear") {
		t.Error("ignored server instructions should be excluded")
	}
	if !strings.Contains(result, "<mcp-server-instructions") {
		t.Error("instructions should be wrapped in tags")
	}
}

func TestIntegration_ResourceSubscribeUnsubscribe(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{
		Resources: &ResourceCapability{Subscribe: true, ListChanged: true},
	}, "")
	ar.setupResourcesList([]ResourceInfo{
		{URI: "file:///test.txt", Name: "test.txt", MimeType: "text/plain"},
	})
	ar.setHandler("resources/templates/list", func(id json.RawMessage, _ json.RawMessage) {
		ar.respond(id, ResourceTemplatesListPage{})
	})
	ar.setHandler("resources/subscribe", func(id json.RawMessage, _ json.RawMessage) {
		ar.respond(id, struct{}{})
	})
	ar.setHandler("resources/unsubscribe", func(id json.RawMessage, _ json.RawMessage) {
		ar.respond(id, struct{}{})
	})

	mgr, server := buildTestServer(t, "res", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	// Enable subscriptions.
	caps := server.client.ServerCapabilities()
	if caps.Resources != nil && caps.Resources.Subscribe {
		server.subscriptions = newSubscriptionManager(server.client)
	}

	if err := server.discoverAndRegisterResources(context.Background()); err != nil {
		t.Fatalf("discoverAndRegisterResources: %v", err)
	}

	// The resource tool should be registered.
	allTools := mgr.toolRegistry.AllWithPrefix("mcp__res__")
	foundResourceTool := false
	for _, tool := range allTools {
		if strings.Contains(tool.Name(), "read_resource") {
			foundResourceTool = true
		}
	}
	if !foundResourceTool {
		names := make([]string, len(allTools))
		for i, tool := range allTools {
			names[i] = tool.Name()
		}
		t.Errorf("expected read_resource tool, got: %v", names)
	}
}

func TestIntegration_ResourceReadExecution(t *testing.T) {
	t.Run("text content", func(t *testing.T) {
		ar := newAutoResponder()
		ar.setupInitialize(ServerCapabilities{
			Resources: &ResourceCapability{Subscribe: true, ListChanged: true},
		}, "")
		ar.setupResourcesList([]ResourceInfo{
			{URI: "file:///hello.txt", Name: "hello.txt", MimeType: "text/plain"},
		})
		ar.setHandler("resources/templates/list", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, ResourceTemplatesListPage{})
		})
		ar.setHandler("resources/subscribe", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, struct{}{})
		})
		ar.setHandler("resources/unsubscribe", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, struct{}{})
		})
		ar.setHandler("resources/read", func(id json.RawMessage, params json.RawMessage) {
			var p struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(params, &p)
			ar.respond(id, ResourceReadResult{
				Contents: []ResourceContent{
					{URI: p.URI, MimeType: "text/plain", Text: "hello from resource"},
				},
			})
		})

		mgr, server := buildTestServer(t, "resread", &config.MCPServerConfig{}, ar)
		defer mgr.Shutdown(context.Background())

		caps := server.client.ServerCapabilities()
		if caps.Resources != nil && caps.Resources.Subscribe {
			server.subscriptions = newSubscriptionManager(server.client)
		}

		if err := server.discoverAndRegisterResources(context.Background()); err != nil {
			t.Fatalf("discoverAndRegisterResources: %v", err)
		}

		tool, ok := mgr.toolRegistry.Get("mcp__resread__read_resource")
		if !ok {
			t.Fatal("read_resource tool not registered")
		}

		richTool, ok := tool.(tools.RichTool)
		if !ok {
			t.Fatal("resource tool should implement RichTool")
		}

		blocks, err := richTool.ExecuteRich(context.Background(), map[string]any{"uri": "file:///hello.txt"})
		if err != nil {
			t.Fatalf("ExecuteRich: %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		if blocks[0].Type != ai.ContentTypeText {
			t.Errorf("expected text type, got %q", blocks[0].Type)
		}
		if blocks[0].Text != "hello from resource" {
			t.Errorf("text = %q, want %q", blocks[0].Text, "hello from resource")
		}

		// Also verify the plain Execute flattens correctly.
		plain, err := tool.Execute(context.Background(), map[string]any{"uri": "file:///hello.txt"})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if plain != "hello from resource" {
			t.Errorf("Execute() = %q, want %q", plain, "hello from resource")
		}
	})

	t.Run("text truncation at 1MB", func(t *testing.T) {
		ar := newAutoResponder()
		ar.setupInitialize(ServerCapabilities{
			Resources: &ResourceCapability{},
		}, "")
		ar.setupResourcesList([]ResourceInfo{
			{URI: "file:///big.txt", Name: "big.txt"},
		})
		ar.setHandler("resources/templates/list", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, ResourceTemplatesListPage{})
		})

		bigText := strings.Repeat("A", maxResourceTextBytes+500)
		ar.setHandler("resources/read", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, ResourceReadResult{
				Contents: []ResourceContent{
					{URI: "file:///big.txt", Text: bigText},
				},
			})
		})

		mgr, server := buildTestServer(t, "resbigtxt", &config.MCPServerConfig{}, ar)
		defer mgr.Shutdown(context.Background())

		if err := server.discoverAndRegisterResources(context.Background()); err != nil {
			t.Fatalf("discoverAndRegisterResources: %v", err)
		}

		tool, ok := mgr.toolRegistry.Get("mcp__resbigtxt__read_resource")
		if !ok {
			t.Fatal("read_resource tool not registered")
		}

		blocks, err := tool.(tools.RichTool).ExecuteRich(context.Background(), map[string]any{"uri": "file:///big.txt"})
		if err != nil {
			t.Fatalf("ExecuteRich: %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		if !strings.Contains(blocks[0].Text, "[truncated at") {
			t.Error("expected truncation marker in oversized text")
		}
		// Content before the truncation marker should be exactly maxResourceTextBytes.
		idx := strings.Index(blocks[0].Text, "\n[truncated at")
		if idx != maxResourceTextBytes {
			t.Errorf("truncation at byte %d, want %d", idx, maxResourceTextBytes)
		}
	})

	t.Run("binary size cap at 512KB", func(t *testing.T) {
		ar := newAutoResponder()
		ar.setupInitialize(ServerCapabilities{
			Resources: &ResourceCapability{},
		}, "")
		ar.setupResourcesList([]ResourceInfo{
			{URI: "file:///huge.bin", Name: "huge.bin", MimeType: "application/octet-stream"},
		})
		ar.setHandler("resources/templates/list", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, ResourceTemplatesListPage{})
		})

		oversizedBlob := base64.StdEncoding.EncodeToString(make([]byte, maxResourceBinaryBytes+100))
		ar.setHandler("resources/read", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, ResourceReadResult{
				Contents: []ResourceContent{
					{URI: "file:///huge.bin", MimeType: "application/octet-stream", Blob: oversizedBlob},
				},
			})
		})

		mgr, server := buildTestServer(t, "resbigbin", &config.MCPServerConfig{}, ar)
		defer mgr.Shutdown(context.Background())

		if err := server.discoverAndRegisterResources(context.Background()); err != nil {
			t.Fatalf("discoverAndRegisterResources: %v", err)
		}

		tool, ok := mgr.toolRegistry.Get("mcp__resbigbin__read_resource")
		if !ok {
			t.Fatal("read_resource tool not registered")
		}

		blocks, err := tool.(tools.RichTool).ExecuteRich(context.Background(), map[string]any{"uri": "file:///huge.bin"})
		if err != nil {
			t.Fatalf("ExecuteRich: %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		if blocks[0].Type != ai.ContentTypeText {
			t.Errorf("expected text type for oversized binary, got %q", blocks[0].Type)
		}
		if !strings.Contains(blocks[0].Text, "too large") {
			t.Errorf("expected 'too large' message, got %q", blocks[0].Text)
		}
	})

	t.Run("image content", func(t *testing.T) {
		ar := newAutoResponder()
		ar.setupInitialize(ServerCapabilities{
			Resources: &ResourceCapability{},
		}, "")
		ar.setupResourcesList([]ResourceInfo{
			{URI: "file:///photo.png", Name: "photo.png", MimeType: "image/png"},
		})
		ar.setHandler("resources/templates/list", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, ResourceTemplatesListPage{})
		})

		imgData := base64.StdEncoding.EncodeToString([]byte("fakepngdata"))
		ar.setHandler("resources/read", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, ResourceReadResult{
				Contents: []ResourceContent{
					{URI: "file:///photo.png", MimeType: "image/png", Blob: imgData},
				},
			})
		})

		mgr, server := buildTestServer(t, "resimg", &config.MCPServerConfig{}, ar)
		defer mgr.Shutdown(context.Background())

		if err := server.discoverAndRegisterResources(context.Background()); err != nil {
			t.Fatalf("discoverAndRegisterResources: %v", err)
		}

		tool, ok := mgr.toolRegistry.Get("mcp__resimg__read_resource")
		if !ok {
			t.Fatal("read_resource tool not registered")
		}

		blocks, err := tool.(tools.RichTool).ExecuteRich(context.Background(), map[string]any{"uri": "file:///photo.png"})
		if err != nil {
			t.Fatalf("ExecuteRich: %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		if blocks[0].Type != ai.ContentTypeImage {
			t.Errorf("expected image type, got %q", blocks[0].Type)
		}
		if blocks[0].MediaType != "image/png" {
			t.Errorf("media type = %q, want image/png", blocks[0].MediaType)
		}
		if blocks[0].ImageData != imgData {
			t.Error("image data mismatch")
		}

		// Plain Execute should produce "[image: image/png]".
		plain, err := tool.Execute(context.Background(), map[string]any{"uri": "file:///photo.png"})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(plain, "[image: image/png]") {
			t.Errorf("Execute() = %q, want '[image: image/png]'", plain)
		}
	})

	t.Run("server error propagates", func(t *testing.T) {
		ar := newAutoResponder()
		ar.setupInitialize(ServerCapabilities{
			Resources: &ResourceCapability{},
		}, "")
		ar.setupResourcesList([]ResourceInfo{
			{URI: "file:///secret.txt", Name: "secret.txt"},
		})
		ar.setHandler("resources/templates/list", func(id json.RawMessage, _ json.RawMessage) {
			ar.respond(id, ResourceTemplatesListPage{})
		})
		ar.setHandler("resources/read", func(id json.RawMessage, _ json.RawMessage) {
			ar.respondError(id, -32600, "access denied")
		})

		mgr, server := buildTestServer(t, "reserr", &config.MCPServerConfig{}, ar)
		defer mgr.Shutdown(context.Background())

		if err := server.discoverAndRegisterResources(context.Background()); err != nil {
			t.Fatalf("discoverAndRegisterResources: %v", err)
		}

		tool, ok := mgr.toolRegistry.Get("mcp__reserr__read_resource")
		if !ok {
			t.Fatal("read_resource tool not registered")
		}

		_, err := tool.(tools.RichTool).ExecuteRich(context.Background(), map[string]any{"uri": "file:///secret.txt"})
		if err == nil {
			t.Fatal("expected error from server")
		}
		if !strings.Contains(err.Error(), "access denied") {
			t.Errorf("error = %v, want 'access denied'", err)
		}
	})
}

func TestIntegration_UnknownNotification(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{}, "")

	mgr, server := buildTestServer(t, "notify", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	// Unknown notifications should be logged and discarded.
	server.handleNotification("notifications/future/feature", json.RawMessage(`{}`))
	server.handleNotification("notifications/progress", json.RawMessage(`{"progress":50}`))
	// No panic = success.
}

func TestIntegration_RootsListRequest(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{}, "")

	mgr, _ := buildTestServer(t, "roots", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	result := mgr.handleRootsList()
	roots, ok := result["roots"].([]map[string]any)
	if !ok || len(roots) != 1 {
		t.Fatal("expected 1 root")
	}
	if roots[0]["uri"] != "file:///test/project" {
		t.Errorf("root URI = %v", roots[0]["uri"])
	}
}

func TestIntegration_ToolNameParsing(t *testing.T) {
	tests := []struct {
		input      string
		wantServer string
		wantTool   string
	}{
		{"mcp__fs__read_file", "fs", "read_file"},
		{"mcp__my-server__my-tool", "my-server", "my-tool"},
		{"bash", "", ""},
		{"mcp__", "", ""},
	}

	for _, tt := range tests {
		server, tool := parseToolName(tt.input)
		if server != tt.wantServer || tool != tt.wantTool {
			t.Errorf("parseToolName(%q) = (%q, %q), want (%q, %q)",
				tt.input, server, tool, tt.wantServer, tt.wantTool)
		}
	}
}

func TestIntegration_BuildToolName(t *testing.T) {
	name := buildToolName("fs", "read_file")
	if name != "mcp__fs__read_file" {
		t.Errorf("buildToolName = %q, want mcp__fs__read_file", name)
	}
}

func TestIntegration_MultipleServers(t *testing.T) {
	reg := tools.NewRegistry()
	skillReg := skill.NewRegistry()

	mgr := NewManager(ManagerConfig{
		ToolRegistry:  reg,
		SkillRegistry: skillReg,
		WorkingDir:    "/test",
		ClientName:    "gi-test",
		ClientVersion: "0.0.1",
	})

	// Server 1.
	ar1 := newAutoResponder()
	ar1.setupInitialize(ServerCapabilities{Tools: &ToolCapability{}}, "Server 1 instructions")
	ar1.setupToolsList([]ToolInfo{
		{Name: "tool_x", Description: "Tool X from S1", InputSchema: map[string]any{"type": "object"}},
	})

	srv1 := newServer("s1", &config.MCPServerConfig{}, ar1, mgr)
	if err := srv1.initialize(context.Background()); err != nil {
		t.Fatalf("s1 initialize: %v", err)
	}
	if err := srv1.discoverAndRegisterTools(context.Background()); err != nil {
		t.Fatalf("s1 discover: %v", err)
	}

	// Server 2.
	ar2 := newAutoResponder()
	ar2.setupInitialize(ServerCapabilities{Tools: &ToolCapability{}}, "Server 2 instructions")
	ar2.setupToolsList([]ToolInfo{
		{Name: "tool_x", Description: "Tool X from S2", InputSchema: map[string]any{"type": "object"}},
	})

	srv2 := newServer("s2", &config.MCPServerConfig{}, ar2, mgr)
	if err := srv2.initialize(context.Background()); err != nil {
		t.Fatalf("s2 initialize: %v", err)
	}
	if err := srv2.discoverAndRegisterTools(context.Background()); err != nil {
		t.Fatalf("s2 discover: %v", err)
	}

	mgr.mu.Lock()
	mgr.servers["s1"] = srv1
	mgr.servers["s2"] = srv2
	mgr.serverList = []string{"s1", "s2"}
	mgr.mu.Unlock()
	defer mgr.Shutdown(context.Background())

	// Both tools should exist.
	t1, ok := reg.Get("mcp__s1__tool_x")
	if !ok {
		t.Fatal("s1 tool_x not found")
	}
	if t1.Description() != "Tool X from S1" {
		t.Errorf("s1 tool description = %q", t1.Description())
	}

	t2, ok := reg.Get("mcp__s2__tool_x")
	if !ok {
		t.Fatal("s2 tool_x not found")
	}
	if t2.Description() != "Tool X from S2" {
		t.Errorf("s2 tool description = %q", t2.Description())
	}

	instr := mgr.ServerInstructions()
	if !strings.Contains(instr, "Server 1 instructions") {
		t.Error("missing s1 instructions")
	}
	if !strings.Contains(instr, "Server 2 instructions") {
		t.Error("missing s2 instructions")
	}
}

func TestIntegration_PromptsListChanged(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{Prompts: &PromptCapability{ListChanged: true}}, "")
	ar.setupPromptsList([]PromptInfo{
		{Name: "greet", Description: "Greeting prompt"},
	})

	mgr, server := buildTestServer(t, "prompts", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	if err := server.discoverAndRegisterPrompts(context.Background()); err != nil {
		t.Fatalf("discoverAndRegisterPrompts: %v", err)
	}

	all := mgr.skillRegistry.AllWithPrefix("mcp__prompts__")
	if len(all) != 1 {
		t.Fatalf("expected 1 prompt skill, got %d", len(all))
	}
	if all[0].Name != "mcp__prompts__greet" {
		t.Errorf("prompt name = %q", all[0].Name)
	}

	// Update prompts.
	ar.setupPromptsList([]PromptInfo{
		{Name: "greet", Description: "Updated greeting"},
		{Name: "farewell", Description: "Farewell prompt"},
	})

	server.handlePromptsListChanged()

	all = mgr.skillRegistry.AllWithPrefix("mcp__prompts__")
	if len(all) != 2 {
		t.Fatalf("expected 2 prompt skills after update, got %d", len(all))
	}

	msgs := mgr.DrainSystemMessages()
	if len(msgs) == 0 {
		t.Fatal("expected system message about prompt update")
	}
	if !strings.Contains(msgs[0], "prompts updated") {
		t.Errorf("system message = %q", msgs[0])
	}
}

func TestIntegration_LogNotification(t *testing.T) {
	ar := newAutoResponder()
	ar.setupInitialize(ServerCapabilities{Logging: &LoggingCapability{}}, "")

	mgr, server := buildTestServer(t, "logger", &config.MCPServerConfig{}, ar)
	defer mgr.Shutdown(context.Background())

	// Valid log should be handled.
	server.handleNotification("notifications/message",
		json.RawMessage(`{"level":"warning","logger":"test","data":"something happened"}`))

	// Malformed log should not crash.
	server.handleNotification("notifications/message", json.RawMessage(`{invalid`))
	// No panic = success.
}

func TestIntegration_ErrorCodeConstants(t *testing.T) {
	if ErrCodeParseError != -32700 {
		t.Errorf("ErrCodeParseError = %d, want -32700", ErrCodeParseError)
	}
	if ErrCodeInvalidRequest != -32600 {
		t.Errorf("ErrCodeInvalidRequest = %d, want -32600", ErrCodeInvalidRequest)
	}
	if ErrCodeMethodNotFound != -32601 {
		t.Errorf("ErrCodeMethodNotFound = %d, want -32601", ErrCodeMethodNotFound)
	}
	if ErrCodeInvalidParams != -32602 {
		t.Errorf("ErrCodeInvalidParams = %d, want -32602", ErrCodeInvalidParams)
	}
	if ErrCodeInternalError != -32603 {
		t.Errorf("ErrCodeInternalError = %d, want -32603", ErrCodeInternalError)
	}

	err := &JSONRPCError{Code: -32050, Message: "custom"}
	if !err.IsServerError() {
		t.Error("code -32050 should be a server error")
	}
	err2 := &JSONRPCError{Code: -32700, Message: "parse error"}
	if err2.IsServerError() {
		t.Error("code -32700 should NOT be a server error")
	}
}

// Ensure unused imports compile (RichToolError checked at error-type level).
var _ = ai.ContentBlock{}
