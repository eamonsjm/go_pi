package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"
	sops "github.com/getsops/sops/v3"
	sopsage "github.com/getsops/sops/v3/age"
	"github.com/getsops/sops/v3/aes"
	"github.com/getsops/sops/v3/decrypt"
	sopsjson "github.com/getsops/sops/v3/stores/json"
)

// configEncryptedRegex limits SOPS encryption to env and headers keys
// within MCP server definitions in settings.json. All other fields remain
// plaintext for readability and diffability.
const configEncryptedRegex = "^(env|headers)$"

// isSopsEncrypted reports whether data contains SOPS encryption metadata,
// indicating some JSON values are encrypted.
func isSopsEncrypted(data []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	_, hasSops := probe["sops"]
	return hasSops
}

// decryptSopsConfig decrypts SOPS-encrypted JSON config data using the
// age identity at keyPath. It returns the plaintext JSON.
func decryptSopsConfig(data []byte, keyPath string) ([]byte, error) {
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read age key %s: %w", keyPath, err)
	}

	// SOPS discovers age identities via the SOPS_AGE_KEY environment variable.
	old, hadOld := os.LookupEnv("SOPS_AGE_KEY")
	if err := os.Setenv("SOPS_AGE_KEY", string(keyData)); err != nil {
		return nil, fmt.Errorf("set SOPS_AGE_KEY: %w", err)
	}
	defer func() {
		if hadOld {
			os.Setenv("SOPS_AGE_KEY", old)
		} else {
			os.Unsetenv("SOPS_AGE_KEY")
		}
	}()

	plain, err := decrypt.Data(data, "json")
	if err != nil {
		return nil, fmt.Errorf("sops decrypt: %w", err)
	}
	return plain, nil
}

// encryptSopsConfig encrypts plaintext JSON config with SOPS, restricting
// encryption to keys matching configEncryptedRegex (env, headers).
// Non-matching keys remain plaintext for readability.
func encryptSopsConfig(data []byte, ageRecipient string) ([]byte, error) {
	store := &sopsjson.Store{}
	branches, err := store.LoadPlainFile(data)
	if err != nil {
		return nil, fmt.Errorf("sops load plain: %w", err)
	}

	masterKey, err := sopsage.MasterKeyFromRecipient(ageRecipient)
	if err != nil {
		return nil, fmt.Errorf("sops age master key: %w", err)
	}

	tree := sops.Tree{
		Branches: branches,
		Metadata: sops.Metadata{
			KeyGroups: []sops.KeyGroup{
				{masterKey},
			},
			Version:        "3.9.0",
			EncryptedRegex: configEncryptedRegex,
		},
	}

	dataKey, errs := tree.GenerateDataKey()
	if len(errs) > 0 {
		return nil, fmt.Errorf("sops generate data key: %v", errs[0])
	}

	tree.Metadata.LastModified = time.Now().UTC()

	cipher := aes.NewCipher()
	unencryptedMac, err := tree.Encrypt(dataKey, cipher)
	if err != nil {
		return nil, fmt.Errorf("sops encrypt: %w", err)
	}

	encryptedMac, err := cipher.Encrypt(
		unencryptedMac,
		dataKey,
		tree.Metadata.LastModified.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("sops encrypt mac: %w", err)
	}
	tree.Metadata.MessageAuthenticationCode = encryptedMac

	out, err := store.EmitEncryptedFile(tree)
	if err != nil {
		return nil, fmt.Errorf("sops emit encrypted: %w", err)
	}
	return out, nil
}

// sopsAgeKeyPath returns the path to the age key file in the config directory.
func sopsAgeKeyPath(configDir string) string {
	return filepath.Join(configDir, "age-key.txt")
}

// loadAgeKeyForConfig reads an age X25519 identity (private key) from a file.
func loadAgeKeyForConfig(path string) (*age.X25519Identity, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open age key %s: %w", path, err)
	}
	defer f.Close()

	identities, err := age.ParseIdentities(f)
	if err != nil {
		return nil, fmt.Errorf("parse age key %s: %w", path, err)
	}
	if len(identities) == 0 {
		return nil, fmt.Errorf("no age identities in %s", path)
	}

	id, ok := identities[0].(*age.X25519Identity)
	if !ok {
		return nil, fmt.Errorf("unexpected identity type in %s", path)
	}
	return id, nil
}

// generateAgeKeyForConfig creates a new age X25519 identity and writes it
// to path with 0600 permissions. Parent directories are created with 0700.
func generateAgeKeyForConfig(path string) (*age.X25519Identity, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("generate age key: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create key dir: %w", err)
	}

	content := fmt.Sprintf("# created: %s\n# public key: %s\n%s\n",
		time.Now().UTC().Format(time.RFC3339),
		id.Recipient().String(),
		id.String(),
	)

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return nil, fmt.Errorf("write age key: %w", err)
	}
	return id, nil
}

// SopsStatus reports the SOPS encryption state of the config directory.
type SopsStatus struct {
	Encrypted    bool   // whether settings.json is currently SOPS-encrypted
	AgeKeyExists bool   // whether the age key file exists
	AgePublicKey string // the age public key (empty if key missing)
	ConfigDir    string // config directory path
}

// ConfigSopsStatus checks the SOPS encryption state of settings.json.
func ConfigSopsStatus(configDir string) (*SopsStatus, error) {
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		configDir = filepath.Join(home, ".gi")
	}

	status := &SopsStatus{ConfigDir: configDir}

	settingsPath := filepath.Join(configDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		status.Encrypted = isSopsEncrypted(data)
	}

	keyPath := sopsAgeKeyPath(configDir)
	id, err := loadAgeKeyForConfig(keyPath)
	if err == nil {
		status.AgeKeyExists = true
		status.AgePublicKey = id.Recipient().String()
	}

	return status, nil
}

// EncryptConfigFile encrypts settings.json in place using SOPS with age.
// Only env and headers fields in MCP server configs are encrypted; all
// other fields remain plaintext. Generates an age key if none exists.
func EncryptConfigFile(configDir string) error {
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		configDir = filepath.Join(home, ".gi")
	}

	settingsPath := filepath.Join(configDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}

	if isSopsEncrypted(data) {
		return fmt.Errorf("settings.json is already encrypted")
	}

	// Ensure age key exists.
	keyPath := sopsAgeKeyPath(configDir)
	id, err := loadAgeKeyForConfig(keyPath)
	if err != nil {
		id, err = generateAgeKeyForConfig(keyPath)
		if err != nil {
			return fmt.Errorf("generate age key: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Generated age key: %s\n", keyPath)
		fmt.Fprintf(os.Stderr, "Public key: %s\n", id.Recipient().String())
		fmt.Fprintf(os.Stderr, "Back up this key! Loss means credential lockout.\n")
	}

	encrypted, err := encryptSopsConfig(data, id.Recipient().String())
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	if err := os.WriteFile(settingsPath, append(encrypted, '\n'), 0o600); err != nil {
		return fmt.Errorf("write encrypted settings: %w", err)
	}

	// Write .sops.yaml template for SOPS CLI compatibility.
	if err := writeSopsYAML(configDir, id.Recipient().String()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write .sops.yaml: %v\n", err)
	}

	return nil
}

// DecryptConfigFile decrypts settings.json in place back to plaintext.
func DecryptConfigFile(configDir string) error {
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		configDir = filepath.Join(home, ".gi")
	}

	settingsPath := filepath.Join(configDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}

	if !isSopsEncrypted(data) {
		return fmt.Errorf("settings.json is not encrypted")
	}

	keyPath := sopsAgeKeyPath(configDir)
	plain, err := decryptSopsConfig(data, keyPath)
	if err != nil {
		return fmt.Errorf("decrypt: %w", err)
	}

	// Re-format as indented JSON for readability.
	var obj interface{}
	if err := json.Unmarshal(plain, &obj); err != nil {
		return fmt.Errorf("parse decrypted config: %w", err)
	}
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("format config: %w", err)
	}

	if err := os.WriteFile(settingsPath, append(formatted, '\n'), 0o600); err != nil {
		return fmt.Errorf("write decrypted settings: %w", err)
	}
	return nil
}

// sopsYAMLTemplate is the .sops.yaml content for SOPS CLI compatibility.
const sopsYAMLTemplate = `# SOPS configuration for gi config files.
# This file enables the SOPS CLI to encrypt/decrypt gi files directly.
# Managed by gi — manual edits may be overwritten.
creation_rules:
  - path_regex: auth\.json$
    age: >-
      %s
  - path_regex: settings\.json$
    encrypted_regex: "^(env|headers)$"
    age: >-
      %s
`

// writeSopsYAML writes a .sops.yaml template for SOPS CLI compatibility.
func writeSopsYAML(configDir, ageRecipient string) error {
	path := filepath.Join(configDir, ".sops.yaml")
	content := fmt.Sprintf(sopsYAMLTemplate, ageRecipient, ageRecipient)
	return os.WriteFile(path, []byte(content), 0o600)
}
