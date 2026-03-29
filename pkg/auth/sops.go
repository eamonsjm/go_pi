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
//
// This uses the SOPS Tree API with explicit key injection rather than the
// SOPS_AGE_KEY environment variable, making it safe for concurrent use.
func decryptSops(data []byte, keyPath string) ([]byte, error) {
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read age key %s: %w", keyPath, err)
	}

	store := &sopsjson.Store{}
	tree, err := store.LoadEncryptedFile(data)
	if err != nil {
		return nil, fmt.Errorf("sops load encrypted: %w", err)
	}

	// Parse the age identity and inject it directly into the master keys,
	// then decrypt the data key without going through the keyservice
	// (which serializes keys via protobuf and loses injected identities).
	var ids sopsage.ParsedIdentities
	if err := ids.Import(string(keyData)); err != nil {
		return nil, fmt.Errorf("parse age identity: %w", err)
	}

	dataKey, err := decryptDataKeyWithIdentities(tree.Metadata.KeyGroups, ids)
	if err != nil {
		return nil, fmt.Errorf("sops get data key: %w", err)
	}

	cipher := aes.NewCipher()
	mac, err := tree.Decrypt(dataKey, cipher)
	if err != nil {
		return nil, fmt.Errorf("sops decrypt: %w", err)
	}

	originalMac, err := cipher.Decrypt(
		tree.Metadata.MessageAuthenticationCode,
		dataKey,
		tree.Metadata.LastModified.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("sops decrypt mac: %w", err)
	}
	if originalMac != mac {
		return nil, fmt.Errorf("sops integrity check failed")
	}

	plain, err := store.EmitPlainFile(tree.Branches)
	if err != nil {
		return nil, fmt.Errorf("sops emit plain: %w", err)
	}
	return plain, nil
}

// decryptDataKeyWithIdentities decrypts the SOPS data key by injecting the
// parsed age identities directly into each age master key and calling Decrypt.
// This bypasses the keyservice RPC path which loses injected identities.
func decryptDataKeyWithIdentities(keyGroups []sops.KeyGroup, ids sopsage.ParsedIdentities) ([]byte, error) {
	for _, kg := range keyGroups {
		for _, k := range kg {
			ak, ok := k.(*sopsage.MasterKey)
			if !ok {
				continue
			}
			ids.ApplyToMasterKey(ak)
			dataKey, err := ak.Decrypt()
			if err == nil {
				return dataKey, nil
			}
		}
	}
	return nil, fmt.Errorf("no age key in metadata could decrypt the data key")
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

// LoadAgeKey reads an age X25519 identity (private key) from a key file.
func LoadAgeKey(path string) (*age.X25519Identity, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open age key %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

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

// GenerateAgeKey creates a new age X25519 identity and writes it to path
// with 0600 permissions. Parent directories are created with 0700 if needed.
func GenerateAgeKey(path string) (*age.X25519Identity, error) {
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
