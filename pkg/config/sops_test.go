package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsSopsEncryptedConfig(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{"plain config", `{"default_provider":"anthropic"}`, false},
		{"sops metadata", `{"default_provider":"anthropic","sops":{"version":"3.9.0"}}`, true},
		{"empty object", `{}`, false},
		{"invalid json", `not json`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSopsEncrypted([]byte(tt.data)); got != tt.want {
				t.Errorf("isSopsEncrypted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEncryptDecryptConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")

	id, err := generateAgeKeyForConfig(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKeyForConfig: %v", err)
	}

	plain := []byte(`{
		"default_provider": "anthropic",
		"mcpServers": {
			"myserver": {
				"command": "npx",
				"args": ["-y", "some-server"],
				"env": {"API_KEY": "sk-secret-123"},
				"headers": {"Authorization": "Bearer tok-secret"},
				"url": "https://example.com/mcp"
			}
		}
	}`)

	// Encrypt.
	encrypted, err := encryptSopsConfig(plain, id.Recipient().String())
	if err != nil {
		t.Fatalf("encryptSopsConfig: %v", err)
	}

	if !isSopsEncrypted(encrypted) {
		t.Fatal("encrypted data should contain SOPS metadata")
	}

	// Decrypt.
	decrypted, err := decryptSopsConfig(encrypted, keyPath)
	if err != nil {
		t.Fatalf("decryptSopsConfig: %v", err)
	}

	// Compare parsed values.
	var orig, result map[string]interface{}
	if err := json.Unmarshal(plain, &orig); err != nil {
		t.Fatalf("parse original: %v", err)
	}
	if err := json.Unmarshal(decrypted, &result); err != nil {
		t.Fatalf("parse decrypted: %v", err)
	}

	// Check non-encrypted fields survived.
	if result["default_provider"] != orig["default_provider"] {
		t.Errorf("default_provider: got %v, want %v", result["default_provider"], orig["default_provider"])
	}

	// Check MCP server env was decrypted correctly.
	servers := result["mcpServers"].(map[string]interface{})
	srv := servers["myserver"].(map[string]interface{})
	env := srv["env"].(map[string]interface{})
	if env["API_KEY"] != "sk-secret-123" {
		t.Errorf("env.API_KEY: got %v, want sk-secret-123", env["API_KEY"])
	}
	headers := srv["headers"].(map[string]interface{})
	if headers["Authorization"] != "Bearer tok-secret" {
		t.Errorf("headers.Authorization: got %v, want Bearer tok-secret", headers["Authorization"])
	}
}

func TestEncryptedRegexSelectivity(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")

	id, err := generateAgeKeyForConfig(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKeyForConfig: %v", err)
	}

	plain := []byte(`{
		"default_provider": "anthropic",
		"mcpServers": {
			"myserver": {
				"command": "npx",
				"env": {"SECRET": "hidden-value"},
				"url": "https://example.com"
			}
		}
	}`)

	encrypted, err := encryptSopsConfig(plain, id.Recipient().String())
	if err != nil {
		t.Fatalf("encryptSopsConfig: %v", err)
	}

	// The encrypted output should contain plaintext for non-encrypted fields.
	encStr := string(encrypted)
	if !strings.Contains(encStr, `"anthropic"`) {
		t.Error("default_provider value should be plaintext in encrypted output")
	}
	if !strings.Contains(encStr, `"npx"`) {
		t.Error("command value should be plaintext in encrypted output")
	}
	if !strings.Contains(encStr, `"https://example.com"`) {
		t.Error("url value should be plaintext in encrypted output")
	}

	// The secret value should be encrypted (not plaintext).
	if strings.Contains(encStr, "hidden-value") {
		t.Error("env value should be encrypted, not plaintext")
	}
}

func TestEncryptDecryptConfigFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	plain := `{
  "default_provider": "anthropic",
  "mcpServers": {
    "test": {
      "command": "echo",
      "env": {"TOKEN": "secret-token"}
    }
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(plain), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Encrypt in place.
	if err := EncryptConfigFile(dir); err != nil {
		t.Fatalf("EncryptConfigFile: %v", err)
	}

	// File should now be SOPS-encrypted.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !isSopsEncrypted(data) {
		t.Fatal("settings.json should be SOPS-encrypted after EncryptConfigFile")
	}

	// Secret should not be in plaintext.
	if strings.Contains(string(data), "secret-token") {
		t.Error("secret-token should be encrypted")
	}

	// Encrypting again should fail.
	if err := EncryptConfigFile(dir); err == nil {
		t.Error("encrypting already-encrypted file should return error")
	}

	// Decrypt in place.
	if err := DecryptConfigFile(dir); err != nil {
		t.Fatalf("DecryptConfigFile: %v", err)
	}

	// File should now be plaintext.
	data, err = os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if isSopsEncrypted(data) {
		t.Fatal("settings.json should be plaintext after DecryptConfigFile")
	}

	// Secret should be back in plaintext.
	if !strings.Contains(string(data), "secret-token") {
		t.Error("secret-token should be plaintext after decryption")
	}

	// Decrypting again should fail.
	if err := DecryptConfigFile(dir); err == nil {
		t.Error("decrypting already-plaintext file should return error")
	}
}

func TestLoadConfigDecryptsSopsSettings(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")

	id, err := generateAgeKeyForConfig(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKeyForConfig: %v", err)
	}

	plain := []byte(`{
		"default_model": "claude-opus-4-6",
		"mcpServers": {
			"secure": {
				"command": "mcp-server",
				"env": {"API_KEY": "sk-secret"},
				"headers": {"X-Auth": "bearer-tok"}
			}
		}
	}`)

	encrypted, err := encryptSopsConfig(plain, id.Recipient().String())
	if err != nil {
		t.Fatalf("encryptSopsConfig: %v", err)
	}

	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, encrypted, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.DefaultModel != "claude-opus-4-6" {
		t.Errorf("DefaultModel: got %q, want claude-opus-4-6", cfg.DefaultModel)
	}

	srv := cfg.MCPServers["secure"]
	if srv == nil {
		t.Fatal("mcpServers.secure should exist")
	}
	if srv.Command != "mcp-server" {
		t.Errorf("command: got %q, want mcp-server", srv.Command)
	}
	if srv.Env["API_KEY"] != "sk-secret" {
		t.Errorf("env.API_KEY: got %q, want sk-secret", srv.Env["API_KEY"])
	}
	if srv.Headers["X-Auth"] != "bearer-tok" {
		t.Errorf("headers.X-Auth: got %q, want bearer-tok", srv.Headers["X-Auth"])
	}

	if !cfg.encrypted {
		t.Error("encrypted flag should be true")
	}
	if cfg.sopsKey == "" {
		t.Error("sopsKey should be set")
	}
}

func TestLoadConfigPlaintextSettings(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	plain := []byte(`{"default_model": "claude-sonnet-4-6"}`)
	if err := os.WriteFile(settingsPath, plain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.DefaultModel != "claude-sonnet-4-6" {
		t.Errorf("DefaultModel: got %q, want claude-sonnet-4-6", cfg.DefaultModel)
	}
	if cfg.encrypted {
		t.Error("encrypted flag should be false for plaintext config")
	}
}

func TestSaveReEncryptsConfig(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")

	id, err := generateAgeKeyForConfig(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKeyForConfig: %v", err)
	}

	plain := []byte(`{
		"default_model": "claude-opus-4-6",
		"mcpServers": {
			"s1": {
				"command": "cmd",
				"env": {"KEY": "original-secret"}
			}
		}
	}`)

	encrypted, err := encryptSopsConfig(plain, id.Recipient().String())
	if err != nil {
		t.Fatalf("encryptSopsConfig: %v", err)
	}

	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, encrypted, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Load (decrypts), modify, save (re-encrypts).
	cfg, err := LoadConfig(WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	cfg.MCPServers["s1"].Env["KEY"] = "updated-secret"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify saved file is SOPS-encrypted.
	savedData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !isSopsEncrypted(savedData) {
		t.Fatal("saved file should be SOPS-encrypted")
	}
	if strings.Contains(string(savedData), "updated-secret") {
		t.Error("updated-secret should not appear in plaintext in encrypted file")
	}

	// Re-load and verify.
	cfg2, err := LoadConfig(WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	srv := cfg2.MCPServers["s1"]
	if srv == nil || srv.Env["KEY"] != "updated-secret" {
		t.Errorf("env.KEY after re-load: got %+v", srv)
	}
}

func TestConfigSopsStatus(t *testing.T) {
	dir := t.TempDir()

	// No settings.json, no key.
	status, err := ConfigSopsStatus(dir)
	if err != nil {
		t.Fatalf("ConfigSopsStatus: %v", err)
	}
	if status.Encrypted {
		t.Error("should not be encrypted when no settings.json")
	}
	if status.AgeKeyExists {
		t.Error("age key should not exist")
	}

	// Create key.
	keyPath := filepath.Join(dir, "age-key.txt")
	id, err := generateAgeKeyForConfig(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKeyForConfig: %v", err)
	}

	status, err = ConfigSopsStatus(dir)
	if err != nil {
		t.Fatalf("ConfigSopsStatus: %v", err)
	}
	if !status.AgeKeyExists {
		t.Error("age key should exist")
	}
	if status.AgePublicKey != id.Recipient().String() {
		t.Errorf("public key: got %q, want %q", status.AgePublicKey, id.Recipient().String())
	}
}

func TestWriteSopsYAML(t *testing.T) {
	dir := t.TempDir()
	recipient := "age1testrecipient"

	if err := writeSopsYAML(dir, recipient); err != nil {
		t.Fatalf("writeSopsYAML: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".sops.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "auth\\.json$") {
		t.Error(".sops.yaml should contain auth.json rule")
	}
	if !strings.Contains(content, "settings\\.json$") {
		t.Error(".sops.yaml should contain settings.json rule")
	}
	if !strings.Contains(content, `encrypted_regex: "^(env|headers)$"`) {
		t.Error(".sops.yaml should contain encrypted_regex for env/headers")
	}
	if !strings.Contains(content, recipient) {
		t.Error(".sops.yaml should contain the age recipient")
	}
}
