package auth

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

// isSopsEncrypted reports whether data contains SOPS encryption metadata,
// indicating the JSON values are encrypted.
func isSopsEncrypted(data []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	_, hasSops := probe["sops"]
	return hasSops
}

// decryptSops decrypts SOPS-encrypted JSON data using the age identity
// at keyPath. It returns the plaintext JSON.
func decryptSops(data []byte, keyPath string) ([]byte, error) {
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read age key %s: %w", keyPath, err)
	}

	// SOPS discovers age identities via the SOPS_AGE_KEY environment variable.
	// Temporarily set it to the key file contents for decryption.
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

// encryptSops encrypts plaintext JSON data with SOPS using an age recipient
// (public key). It returns the encrypted JSON with SOPS metadata.
func encryptSops(data []byte, ageRecipient string) ([]byte, error) {
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
			Version: "3.9.0",
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

	// The MAC itself must be encrypted with the data key.
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

// loadAgeKey reads an age X25519 identity (private key) from a key file.
func loadAgeKey(path string) (*age.X25519Identity, error) {
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

// generateAgeKey creates a new age X25519 identity and writes it to path
// with 0600 permissions. Parent directories are created with 0700 if needed.
func generateAgeKey(path string) (*age.X25519Identity, error) {
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
