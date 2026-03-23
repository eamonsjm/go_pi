package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ejm/go_pi/pkg/config"
)

// expandEnvVars replaces ${VAR_NAME} references in s with values from the
// process environment. If allowlist is non-nil, only variables in the
// allowlist are expanded (for project-level security). Unresolved variables
// expand to empty string with a warning log.
func expandEnvVars(s string, allowlist map[string]bool) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		// Look for "${".
		idx := strings.Index(s[i:], "${")
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+idx])
		i += idx + 2 // skip "${"

		// Find closing "}".
		end := strings.Index(s[i:], "}")
		if end < 0 {
			// No closing brace; write the rest as-is.
			b.WriteString("${")
			b.WriteString(s[i:])
			break
		}

		varName := s[i : i+end]
		i += end + 1 // skip past "}"

		// Check allowlist if provided.
		if allowlist != nil && !allowlist[varName] {
			log.Printf("mcp: env var ${%s} not in project allowlist, expanding to empty", varName)
			continue
		}

		val, ok := os.LookupEnv(varName)
		if !ok {
			log.Printf("mcp: env var ${%s} not set, expanding to empty", varName)
		}
		b.WriteString(val)
	}
	return b.String()
}

// expandServerConfig applies environment variable interpolation to all
// string fields of an MCPServerConfig. If allowlist is non-nil, only
// allowlisted variables are interpolated.
func expandServerConfig(cfg *config.MCPServerConfig, allowlist map[string]bool) *config.MCPServerConfig {
	out := *cfg // shallow copy
	out.Command = expandEnvVars(out.Command, allowlist)
	out.URL = expandEnvVars(out.URL, allowlist)
	out.Instructions = expandEnvVars(out.Instructions, allowlist)

	if len(cfg.Args) > 0 {
		out.Args = make([]string, len(cfg.Args))
		for i, a := range cfg.Args {
			out.Args[i] = expandEnvVars(a, allowlist)
		}
	}
	if len(cfg.Env) > 0 {
		out.Env = make(map[string]string, len(cfg.Env))
		for k, v := range cfg.Env {
			out.Env[k] = expandEnvVars(v, allowlist)
		}
	}
	if len(cfg.Headers) > 0 {
		out.Headers = make(map[string]string, len(cfg.Headers))
		for k, v := range cfg.Headers {
			out.Headers[k] = expandEnvVars(v, allowlist)
		}
	}
	return &out
}

// approvalCacheEntry is one entry in the approved MCP servers cache.
type approvalCacheEntry struct {
	ProjectPath string `json:"project_path"`
	ServerName  string `json:"server_name"`
	CommandHash string `json:"command_hash,omitempty"` // SHA-256 of command+args (stdio)
	HostPort    string `json:"host_port,omitempty"`    // host:port (HTTP)
}

// approvalCache holds approved project-level MCP server connections.
type approvalCache struct {
	Approved []approvalCacheEntry `json:"approved"`
}

// approvalCachePath returns the path to the approval cache file.
func approvalCachePath(configDir string) string {
	return filepath.Join(configDir, "approved_mcp_servers.json")
}

// loadApprovalCache loads the approval cache from disk. Returns empty cache
// if the file doesn't exist.
func loadApprovalCache(configDir string) (*approvalCache, error) {
	data, err := os.ReadFile(approvalCachePath(configDir))
	if err != nil {
		if os.IsNotExist(err) {
			return &approvalCache{}, nil
		}
		return nil, fmt.Errorf("reading approval cache: %w", err)
	}
	var cache approvalCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("parsing approval cache: %w", err)
	}
	return &cache, nil
}

// saveApprovalCache writes the approval cache to disk.
func saveApprovalCache(configDir string, cache *approvalCache) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling approval cache: %w", err)
	}
	return os.WriteFile(approvalCachePath(configDir), data, 0o600)
}

// isApproved checks if a project-level server is in the approval cache.
func (c *approvalCache) isApproved(projectPath, serverName string, cfg *config.MCPServerConfig) bool {
	key := serverKey(cfg)
	for _, e := range c.Approved {
		if e.ProjectPath == projectPath && e.ServerName == serverName {
			if cfg.URL != "" {
				return e.HostPort == key
			}
			return e.CommandHash == key
		}
	}
	return false
}

// approve adds a server to the approval cache.
func (c *approvalCache) approve(projectPath, serverName string, cfg *config.MCPServerConfig) {
	entry := approvalCacheEntry{
		ProjectPath: projectPath,
		ServerName:  serverName,
	}
	if cfg.URL != "" {
		entry.HostPort = serverKey(cfg)
	} else {
		entry.CommandHash = serverKey(cfg)
	}
	c.Approved = append(c.Approved, entry)
}

// serverKey returns the cache key for a server config: SHA-256 hash of
// command+args for stdio, or host:port for HTTP. Only host:port is used
// for HTTP so that path/query (which may contain interpolated secrets)
// do not affect the cache key.
func serverKey(cfg *config.MCPServerConfig) string {
	if cfg.URL != "" {
		u, err := url.Parse(cfg.URL)
		if err != nil {
			return cfg.URL // fallback to full URL if unparseable
		}
		return u.Host
	}
	h := sha256.New()
	h.Write([]byte(cfg.Command))
	for _, a := range cfg.Args {
		h.Write([]byte{0})
		h.Write([]byte(a))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
