package mcp

import (
	"context"
	"testing"

	"github.com/ejm/go_pi/pkg/config"
)

func TestPermissionHook_NonMCPTool(t *testing.T) {
	h := NewPermissionHook(nil, nil)
	err := h.BeforeExecute(context.Background(), "Bash", nil)
	if err != nil {
		t.Errorf("non-MCP tool should pass: %v", err)
	}
}

func TestPermissionHook_Deny(t *testing.T) {
	configs := map[string]*config.MCPPermissionConfig{
		"fs": {Deny: []string{"delete_file"}},
	}
	h := NewPermissionHook(configs, nil)

	err := h.BeforeExecute(context.Background(), "mcp__fs__delete_file", nil)
	if err == nil {
		t.Error("denied tool should return error")
	}
}

func TestPermissionHook_DenyTakesPrecedence(t *testing.T) {
	configs := map[string]*config.MCPPermissionConfig{
		"fs": {
			AutoApprove: []string{"delete_file"},
			Deny:        []string{"delete_file"},
		},
	}
	h := NewPermissionHook(configs, nil)

	err := h.BeforeExecute(context.Background(), "mcp__fs__delete_file", nil)
	if err == nil {
		t.Error("deny should take precedence over auto_approve")
	}
}

func TestPermissionHook_AutoApprove(t *testing.T) {
	configs := map[string]*config.MCPPermissionConfig{
		"fs": {AutoApprove: []string{"read_file", "list_dir"}},
	}
	h := NewPermissionHook(configs, nil)

	err := h.BeforeExecute(context.Background(), "mcp__fs__read_file", nil)
	if err != nil {
		t.Errorf("auto-approved tool should pass: %v", err)
	}
}

func TestPermissionHook_ReadOnlyAnnotation(t *testing.T) {
	h := NewPermissionHook(nil, nil)

	readOnly := true
	h.SetAnnotationSource(NewAnnotationLookup(func(server, tool string) *ToolAnnotations {
		if server == "fs" && tool == "read_file" {
			return &ToolAnnotations{ReadOnlyHint: &readOnly}
		}
		return nil
	}))

	err := h.BeforeExecute(context.Background(), "mcp__fs__read_file", nil)
	if err != nil {
		t.Errorf("read-only tool should pass: %v", err)
	}
}

func TestPermissionHook_DefaultRequiresConfirmation(t *testing.T) {
	// No config, no annotations, no confirm callback → deny.
	h := NewPermissionHook(nil, nil)

	err := h.BeforeExecute(context.Background(), "mcp__fs__write_file", nil)
	if err == nil {
		t.Error("should require confirmation and fail without callback")
	}
}

func TestPermissionHook_ConfirmApproved(t *testing.T) {
	h := NewPermissionHook(nil, func(server, tool, desc string) (bool, error) {
		return true, nil
	})

	err := h.BeforeExecute(context.Background(), "mcp__fs__write_file", nil)
	if err != nil {
		t.Errorf("confirmed tool should pass: %v", err)
	}
}

func TestPermissionHook_ConfirmDenied(t *testing.T) {
	h := NewPermissionHook(nil, func(server, tool, desc string) (bool, error) {
		return false, nil
	})

	err := h.BeforeExecute(context.Background(), "mcp__fs__write_file", nil)
	if err == nil {
		t.Error("user-denied tool should return error")
	}
}

func TestPermissionHook_AfterExecute(t *testing.T) {
	h := NewPermissionHook(nil, nil)
	result, err := h.AfterExecute(context.Background(), "mcp__fs__read", nil, "hello", nil)
	if result != "hello" || err != nil {
		t.Errorf("AfterExecute should pass through: result=%q, err=%v", result, err)
	}
}

func TestPermissionHook_UnknownServer(t *testing.T) {
	// Config for one server, tool on another server.
	configs := map[string]*config.MCPPermissionConfig{
		"fs": {AutoApprove: []string{"read_file"}},
	}
	h := NewPermissionHook(configs, nil)

	// Tool on "other" server — no config, no confirm → deny.
	err := h.BeforeExecute(context.Background(), "mcp__other__some_tool", nil)
	if err == nil {
		t.Error("tool on unconfigured server should require confirmation")
	}
}

func TestPermissionHook_SetConfirm(t *testing.T) {
	// Start with nil confirm (non-interactive) → deny.
	h := NewPermissionHook(nil, nil)
	err := h.BeforeExecute(context.Background(), "mcp__fs__write_file", nil)
	if err == nil {
		t.Error("should deny without confirm callback")
	}

	// Wire a confirm callback after construction → approve.
	h.SetConfirm(func(server, tool, desc string) (bool, error) {
		return true, nil
	})
	err = h.BeforeExecute(context.Background(), "mcp__fs__write_file", nil)
	if err != nil {
		t.Errorf("should approve after SetConfirm: %v", err)
	}
}

func TestAnnotationLookup(t *testing.T) {
	readOnly := true
	src := NewAnnotationLookup(func(server, tool string) *ToolAnnotations {
		if server == "s" && tool == "t" {
			return &ToolAnnotations{ReadOnlyHint: &readOnly}
		}
		return nil
	})

	a := src.GetAnnotations("s", "t")
	if a == nil || !AnnotationReadOnly(a) {
		t.Error("expected read-only annotation")
	}

	a = src.GetAnnotations("s", "other")
	if a != nil {
		t.Error("expected nil for unknown tool")
	}
}

func TestAnnotationLookup_Nil(t *testing.T) {
	src := NewAnnotationLookup(nil)
	a := src.GetAnnotations("s", "t")
	if a != nil {
		t.Error("expected nil with nil function")
	}
}
