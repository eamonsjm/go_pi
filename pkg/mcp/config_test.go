package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ejm/go_pi/pkg/config"
)

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("TEST_VAR", "hello")
	t.Setenv("EMPTY_VAR", "")

	tests := []struct {
		name      string
		input     string
		allowlist map[string]bool
		want      string
	}{
		{
			name:  "no vars",
			input: "plain text",
			want:  "plain text",
		},
		{
			name:  "single var",
			input: "prefix ${TEST_VAR} suffix",
			want:  "prefix hello suffix",
		},
		{
			name:  "multiple vars",
			input: "${TEST_VAR}/${TEST_VAR}",
			want:  "hello/hello",
		},
		{
			name:  "unset var expands to empty",
			input: "before ${UNSET_VAR_XYZ} after",
			want:  "before  after",
		},
		{
			name:  "unclosed brace",
			input: "before ${UNCLOSED",
			want:  "before ${UNCLOSED",
		},
		{
			name:      "allowlist blocks disallowed vars",
			input:     "${TEST_VAR} ${BLOCKED}",
			allowlist: map[string]bool{"BLOCKED": true},
			want:      " ",
		},
		{
			name:      "allowlist permits allowed vars",
			input:     "${TEST_VAR}",
			allowlist: map[string]bool{"TEST_VAR": true},
			want:      "hello",
		},
		{
			name:  "empty var",
			input: "${EMPTY_VAR}",
			want:  "",
		},
		{
			name:  "adjacent vars",
			input: "${TEST_VAR}${TEST_VAR}",
			want:  "hellohello",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandEnvVars(tt.input, tt.allowlist)
			if got != tt.want {
				t.Errorf("expandEnvVars(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExpandServerConfig(t *testing.T) {
	t.Setenv("CMD_PATH", "/usr/bin/mcp")
	t.Setenv("DB_NAME", "testdb")
	t.Setenv("API_KEY", "secret123")

	cfg := &config.MCPServerConfig{
		Command: "${CMD_PATH}",
		Args:    []string{"--db=${DB_NAME}", "--verbose"},
		Env:     map[string]string{"KEY": "${API_KEY}"},
		Headers: map[string]string{"Authorization": "Bearer ${API_KEY}"},
		URL:     "https://${DB_NAME}.example.com",
	}

	got := expandServerConfig(cfg, nil)

	if got.Command != "/usr/bin/mcp" {
		t.Errorf("Command = %q, want %q", got.Command, "/usr/bin/mcp")
	}
	if got.Args[0] != "--db=testdb" {
		t.Errorf("Args[0] = %q, want %q", got.Args[0], "--db=testdb")
	}
	if got.Args[1] != "--verbose" {
		t.Errorf("Args[1] = %q, want %q", got.Args[1], "--verbose")
	}
	if got.Env["KEY"] != "secret123" {
		t.Errorf("Env[KEY] = %q, want %q", got.Env["KEY"], "secret123")
	}
	if got.Headers["Authorization"] != "Bearer secret123" {
		t.Errorf("Headers[Authorization] = %q, want %q", got.Headers["Authorization"], "Bearer secret123")
	}
	if got.URL != "https://testdb.example.com" {
		t.Errorf("URL = %q, want %q", got.URL, "https://testdb.example.com")
	}

	// Original must be unchanged.
	if cfg.Command != "${CMD_PATH}" {
		t.Error("expandServerConfig mutated original config")
	}
}

func TestApprovalCache(t *testing.T) {
	dir := t.TempDir()

	// Load from non-existent file should return empty cache.
	cache, err := loadApprovalCache(dir)
	if err != nil {
		t.Fatalf("loadApprovalCache: %v", err)
	}
	if len(cache.Approved) != 0 {
		t.Fatalf("expected empty cache, got %d entries", len(cache.Approved))
	}

	// Approve a stdio server.
	stdioCfg := &config.MCPServerConfig{Command: "npx", Args: []string{"-y", "server"}}
	cache.approve("/project/a", "fs", stdioCfg)

	// Approve an HTTP server.
	httpCfg := &config.MCPServerConfig{URL: "https://mcp.example.com/mcp"}
	cache.approve("/project/a", "remote", httpCfg)

	// Save and reload.
	if err := saveApprovalCache(dir, cache); err != nil {
		t.Fatalf("saveApprovalCache: %v", err)
	}

	loaded, err := loadApprovalCache(dir)
	if err != nil {
		t.Fatalf("loadApprovalCache: %v", err)
	}
	if len(loaded.Approved) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded.Approved))
	}

	// Check approval.
	if !loaded.isApproved("/project/a", "fs", stdioCfg) {
		t.Error("expected stdio server to be approved")
	}
	if !loaded.isApproved("/project/a", "remote", httpCfg) {
		t.Error("expected HTTP server to be approved")
	}

	// Different project should not be approved.
	if loaded.isApproved("/project/b", "fs", stdioCfg) {
		t.Error("expected different project to not be approved")
	}

	// Different command should not be approved.
	differentCfg := &config.MCPServerConfig{Command: "npx", Args: []string{"-y", "other-server"}}
	if loaded.isApproved("/project/a", "fs", differentCfg) {
		t.Error("expected different command to not be approved")
	}

	// Verify file exists.
	if _, err := os.Stat(filepath.Join(dir, "approved_mcp_servers.json")); err != nil {
		t.Errorf("approval cache file not found: %v", err)
	}
}

func TestExpandServerConfigRespectsOrigin(t *testing.T) {
	t.Setenv("ALLOWED_VAR", "allowed-value")
	t.Setenv("BLOCKED_VAR", "blocked-value")

	allowlist := map[string]bool{"ALLOWED_VAR": true}

	// Project-origin server: only allowlisted vars expand.
	projectCfg := &config.MCPServerConfig{
		Origin:  "project",
		Command: "${ALLOWED_VAR}",
		Args:    []string{"--secret=${BLOCKED_VAR}"},
	}

	var envAllowlist map[string]bool
	if projectCfg.Origin == "project" {
		envAllowlist = allowlist
	}
	expanded := expandServerConfig(projectCfg, envAllowlist)

	if expanded.Command != "allowed-value" {
		t.Errorf("project: allowed var not expanded: Command = %q, want %q", expanded.Command, "allowed-value")
	}
	if expanded.Args[0] != "--secret=" {
		t.Errorf("project: blocked var should expand to empty: Args[0] = %q, want %q", expanded.Args[0], "--secret=")
	}

	// Global-origin server: all vars expand (nil allowlist).
	globalCfg := &config.MCPServerConfig{
		Origin:  "global",
		Command: "${ALLOWED_VAR}",
		Args:    []string{"--secret=${BLOCKED_VAR}"},
	}

	envAllowlist = nil
	if globalCfg.Origin == "project" {
		envAllowlist = allowlist
	}
	expanded = expandServerConfig(globalCfg, envAllowlist)

	if expanded.Command != "allowed-value" {
		t.Errorf("global: Command = %q, want %q", expanded.Command, "allowed-value")
	}
	if expanded.Args[0] != "--secret=blocked-value" {
		t.Errorf("global: blocked var should expand freely: Args[0] = %q, want %q", expanded.Args[0], "--secret=blocked-value")
	}
}

func TestServerKey(t *testing.T) {
	// HTTP server uses host:port as key (path/query stripped).
	httpCfg := &config.MCPServerConfig{URL: "https://example.com/mcp"}
	if got := serverKey(httpCfg); got != "example.com" {
		t.Errorf("HTTP serverKey = %q, want %q", got, "example.com")
	}

	// HTTP with explicit port.
	httpCfgPort := &config.MCPServerConfig{URL: "https://example.com:8443/v2/mcp?token=secret"}
	if got := serverKey(httpCfgPort); got != "example.com:8443" {
		t.Errorf("HTTP serverKey with port = %q, want %q", got, "example.com:8443")
	}

	// Different paths on same host produce the same key.
	httpCfg2 := &config.MCPServerConfig{URL: "https://example.com/other"}
	if serverKey(httpCfg) != serverKey(httpCfg2) {
		t.Error("same host with different paths should produce the same key")
	}

	// Stdio server uses command hash.
	stdioCfg := &config.MCPServerConfig{Command: "npx", Args: []string{"-y", "server"}}
	key := serverKey(stdioCfg)
	if len(key) != 64 { // SHA-256 hex
		t.Errorf("Stdio serverKey length = %d, want 64", len(key))
	}

	// Different args produce different hash.
	stdioCfg2 := &config.MCPServerConfig{Command: "npx", Args: []string{"-y", "other"}}
	if serverKey(stdioCfg) == serverKey(stdioCfg2) {
		t.Error("different args should produce different keys")
	}
}
