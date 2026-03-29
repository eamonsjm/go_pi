package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/auth"
)

// ---------------------------------------------------------------------------
// NewEncryptCommand
// ---------------------------------------------------------------------------

func TestNewEncryptCommand_Metadata(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewEncryptCommand(store)

	if cmd.Name != "encrypt" {
		t.Errorf("Name = %q, want %q", cmd.Name, "encrypt")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewEncryptCommand_GeneratesKeyAndEncrypts(t *testing.T) {
	store := testAuthStore(t)
	// Add a credential so there's something to encrypt.
	store.Set("anthropic", &auth.Credential{
		Type: auth.CredentialAPIKey,
		Key:  "sk-test-key-123",
	})
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := NewEncryptCommand(store)
	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "encrypted with SOPS") {
		t.Errorf("expected success message, got %q", result.Text)
	}
	if !strings.Contains(result.Text, "Back up your age key") {
		t.Errorf("expected backup warning, got %q", result.Text)
	}

	// Verify the auth.json file is now SOPS-encrypted.
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "sops") {
		t.Error("auth.json should be SOPS-encrypted after /encrypt")
	}

	// Verify sops-config.json was created.
	cfg, err := auth.LoadSopsConfig(store.ConfigDir())
	if err != nil {
		t.Fatalf("LoadSopsConfig: %v", err)
	}
	if !cfg.Enabled {
		t.Error("sops-config should be enabled after /encrypt")
	}
	if cfg.AgeKeyPath == "" {
		t.Error("sops-config should have AgeKeyPath set")
	}

	// Verify age key was generated.
	if _, err := os.Stat(store.AgeKeyPath()); err != nil {
		t.Errorf("age key file should exist at %s: %v", store.AgeKeyPath(), err)
	}

	// Verify store state.
	if store.SopsKey() == "" {
		t.Error("store.SopsKey() should be non-empty after encrypt")
	}
	if !store.Encrypted() {
		t.Error("store.Encrypted() should be true after encrypt")
	}
}

func TestNewEncryptCommand_AlreadyEncrypted(t *testing.T) {
	store := testAuthStore(t)
	store.Set("anthropic", &auth.Credential{
		Type: auth.CredentialAPIKey,
		Key:  "sk-test",
	})

	// Encrypt first.
	cmd := NewEncryptCommand(store)
	teaCmd := cmd.Execute("")
	msg := teaCmd()
	first := msg.(CommandResultMsg)
	if first.IsError {
		t.Fatalf("first encrypt failed: %s", first.Text)
	}

	// Run again — should report already encrypted.
	teaCmd = cmd.Execute("")
	msg = teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "already encrypted") {
		t.Errorf("expected 'already encrypted' message, got %q", result.Text)
	}
}

func TestNewEncryptCommand_UsesExistingKey(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")

	// Pre-generate an age key.
	keyPath := filepath.Join(dir, "age-key.txt")
	existingID, err := auth.GenerateAgeKey(keyPath)
	if err != nil {
		t.Fatalf("GenerateAgeKey: %v", err)
	}

	store, err := auth.NewStore(authPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.Set("test", &auth.Credential{
		Type: auth.CredentialAPIKey,
		Key:  "sk-test",
	})
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := NewEncryptCommand(store)
	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Text)
	}

	// Should use the existing key, not generate a new one.
	if !strings.Contains(result.Text, existingID.Recipient().String()) {
		t.Errorf("expected public key %s in result, got %q",
			existingID.Recipient().String(), result.Text)
	}
}

// ---------------------------------------------------------------------------
// NewDecryptCommand
// ---------------------------------------------------------------------------

func TestNewDecryptCommand_Metadata(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewDecryptCommand(store)

	if cmd.Name != "decrypt" {
		t.Errorf("Name = %q, want %q", cmd.Name, "decrypt")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewDecryptCommand_NotEncrypted(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewDecryptCommand(store)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "not encrypted") {
		t.Errorf("expected 'not encrypted' message, got %q", result.Text)
	}
}

func TestNewDecryptCommand_WarnsWithoutForce(t *testing.T) {
	store := testAuthStore(t)
	store.Set("anthropic", &auth.Credential{
		Type: auth.CredentialAPIKey,
		Key:  "sk-test-decrypt",
	})
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// First encrypt.
	encCmd := NewEncryptCommand(store)
	teaCmd := encCmd.Execute("")
	msg := teaCmd()
	encResult := msg.(CommandResultMsg)
	if encResult.IsError {
		t.Fatalf("encrypt failed: %s", encResult.Text)
	}
	savedKey := store.SopsKey()

	// Attempt decrypt without --force.
	decCmd := NewDecryptCommand(store)
	teaCmd = decCmd.Execute("")
	msg = teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Error("warning should not be an error")
	}
	if !strings.Contains(result.Text, "--force") {
		t.Error("warning should mention --force flag")
	}
	if !strings.Contains(result.Text, "plaintext") {
		t.Error("warning should mention plaintext")
	}
	if !strings.Contains(result.Text, "age key") {
		t.Error("warning should mention the age key")
	}

	// Store should still be encrypted.
	if store.SopsKey() != savedKey {
		t.Error("SopsKey should be unchanged after warning")
	}
	if !store.Encrypted() {
		t.Error("store should still be encrypted after warning")
	}

	// File should still be encrypted.
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "sops") {
		t.Error("auth.json should still be encrypted after warning")
	}
}

func TestNewDecryptCommand_DecryptsWithForce(t *testing.T) {
	store := testAuthStore(t)
	store.Set("anthropic", &auth.Credential{
		Type: auth.CredentialAPIKey,
		Key:  "sk-test-decrypt",
	})
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// First encrypt.
	encCmd := NewEncryptCommand(store)
	teaCmd := encCmd.Execute("")
	msg := teaCmd()
	encResult := msg.(CommandResultMsg)
	if encResult.IsError {
		t.Fatalf("encrypt failed: %s", encResult.Text)
	}

	// Verify file is encrypted.
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "sops") {
		t.Fatal("file should be encrypted before decrypt test")
	}

	// Now decrypt with --force.
	decCmd := NewDecryptCommand(store)
	teaCmd = decCmd.Execute("--force")
	msg = teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Fatalf("decrypt error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "decrypted") {
		t.Errorf("expected 'decrypted' message, got %q", result.Text)
	}
	if !strings.Contains(result.Text, "/encrypt") {
		t.Errorf("expected re-encrypt hint, got %q", result.Text)
	}
	if !strings.Contains(result.Text, "age key") {
		t.Error("success message should mention the age key")
	}

	// Verify file is now plaintext.
	data, err = os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "sops") {
		t.Error("auth.json should be plaintext after /decrypt --force")
	}
	// Verify credential is preserved.
	if !strings.Contains(string(data), "sk-test-decrypt") {
		t.Error("credential should be present in plaintext file")
	}

	// Verify sops-config.json was removed.
	cfg, err := auth.LoadSopsConfig(store.ConfigDir())
	if err != nil {
		t.Fatalf("LoadSopsConfig: %v", err)
	}
	if cfg.Enabled {
		t.Error("sops-config should be disabled after /decrypt --force")
	}

	// Verify store state.
	if store.SopsKey() != "" {
		t.Errorf("store.SopsKey() should be empty after decrypt, got %q", store.SopsKey())
	}
	if store.Encrypted() {
		t.Error("store.Encrypted() should be false after decrypt")
	}
}

// ---------------------------------------------------------------------------
// Encrypt → Decrypt → Encrypt round-trip
// ---------------------------------------------------------------------------

func TestEncryptDecryptRoundTrip(t *testing.T) {
	store := testAuthStore(t)
	store.Set("provider-a", &auth.Credential{
		Type: auth.CredentialAPIKey,
		Key:  "sk-round-trip-a",
	})
	store.Set("provider-b", &auth.Credential{
		Type: auth.CredentialOAuth,
		AccessToken: "tok-round-trip-b",
	})
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	encCmd := NewEncryptCommand(store)
	decCmd := NewDecryptCommand(store)

	// Step 1: Encrypt.
	msg := encCmd.Execute("")()
	r := msg.(CommandResultMsg)
	if r.IsError {
		t.Fatalf("encrypt: %s", r.Text)
	}

	// Step 2: Decrypt (requires --force).
	msg = decCmd.Execute("--force")()
	r = msg.(CommandResultMsg)
	if r.IsError {
		t.Fatalf("decrypt: %s", r.Text)
	}

	// Verify credentials survived the round-trip.
	store2, err := auth.NewStore(store.Path())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	credA := store2.Get("provider-a")
	if credA == nil || credA.Key != "sk-round-trip-a" {
		t.Errorf("provider-a credential lost after round-trip: %+v", credA)
	}
	credB := store2.Get("provider-b")
	if credB == nil || credB.AccessToken != "tok-round-trip-b" {
		t.Errorf("provider-b credential lost after round-trip: %+v", credB)
	}

	// Step 3: Re-encrypt.
	msg = encCmd.Execute("")()
	r = msg.(CommandResultMsg)
	if r.IsError {
		t.Fatalf("re-encrypt: %s", r.Text)
	}

	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "sops") {
		t.Error("file should be SOPS-encrypted after re-encrypt")
	}
}

// ---------------------------------------------------------------------------
// NewSopsStatusCommand
// ---------------------------------------------------------------------------

func TestNewSopsStatusCommand_Metadata(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewSopsStatusCommand(store)

	if cmd.Name != "sops-status" {
		t.Errorf("Name = %q, want %q", cmd.Name, "sops-status")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewSopsStatusCommand_Plaintext(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewSopsStatusCommand(store)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "plaintext") {
		t.Errorf("expected 'plaintext' in status, got %q", result.Text)
	}
	if !strings.Contains(result.Text, "not enabled") {
		t.Errorf("expected 'not enabled' in status, got %q", result.Text)
	}
}

func TestNewSopsStatusCommand_Encrypted(t *testing.T) {
	store := testAuthStore(t)
	store.Set("test", &auth.Credential{Type: auth.CredentialAPIKey, Key: "sk-test"})
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Encrypt first.
	encCmd := NewEncryptCommand(store)
	msg := encCmd.Execute("")()
	if r := msg.(CommandResultMsg); r.IsError {
		t.Fatalf("encrypt: %s", r.Text)
	}

	cmd := NewSopsStatusCommand(store)
	teaCmd := cmd.Execute("")
	msg = teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "encrypted") {
		t.Errorf("expected 'encrypted' in status, got %q", result.Text)
	}
	if !strings.Contains(result.Text, "Active key:") {
		t.Errorf("expected 'Active key:' in status, got %q", result.Text)
	}
}

// ---------------------------------------------------------------------------
// NewExportAgeKeyCommand
// ---------------------------------------------------------------------------

func TestNewExportAgeKeyCommand_Metadata(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewExportAgeKeyCommand(store)

	if cmd.Name != "export-age-key" {
		t.Errorf("Name = %q, want %q", cmd.Name, "export-age-key")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewExportAgeKeyCommand_NoKey(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewExportAgeKeyCommand(store)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error when no age key exists")
	}
	if !strings.Contains(result.Text, "/encrypt") {
		t.Errorf("expected hint to run /encrypt, got %q", result.Text)
	}
}

func TestNewExportAgeKeyCommand_WithKey(t *testing.T) {
	store := testAuthStore(t)

	// Generate an age key at the expected path.
	id, err := auth.GenerateAgeKey(store.AgeKeyPath())
	if err != nil {
		t.Fatalf("GenerateAgeKey: %v", err)
	}

	cmd := NewExportAgeKeyCommand(store)
	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, id.Recipient().String()) {
		t.Errorf("expected public key in output, got %q", result.Text)
	}
}
