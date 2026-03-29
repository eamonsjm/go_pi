# SOPS Migration Plan: Encrypting Secrets at Rest

## Problem

`~/.gi/auth.json` stores API keys, OAuth access tokens, and refresh tokens as
plaintext JSON. Anyone (or any process) with read access to the file can
exfiltrate every credential. File permissions (0600) are the only protection —
no encryption at rest.

Secondary exposure: MCP server configs in `~/.gi/settings.json` can carry
secrets in `env` and `headers` fields, also stored as plaintext.

## Current Architecture

```
~/.gi/
├── auth.json          ← API keys + OAuth tokens (PLAINTEXT)
├── auth.json.lock     ← flock-based concurrency lock
├── settings.json      ← global config (may contain MCP secrets)
└── sessions/          ← conversation history
```

**auth.json format (current):**
```json
{
  "anthropic": {
    "type": "api_key",
    "key": "sk-ant-api03-XXXX"
  },
  "openai": {
    "type": "oauth",
    "refresh_token": "rt-XXXX",
    "access_token": "at-XXXX",
    "expires_at": 1711584000000
  }
}
```

**Relevant code paths:**
- `pkg/auth/store.go` — `Store.Load()` / `Store.Save()` read/write auth.json
- `pkg/auth/store_unix.go` — file permission checks, flock locking
- `pkg/auth/store_windows.go` — Windows locking equivalent
- `pkg/auth/credential.go` — `Credential` struct, `KeyResolver` (`!command` support)
- `pkg/auth/resolver.go` — resolution chain (override → store → OAuth → env)
- `pkg/config/config.go` — `LoadConfig` merges global + project settings
- `pkg/config/auth.go` — provider env var mappings

**Existing mitigations (insufficient):**
- 0600 file permissions (bypassed by root, malware, disk access, backups)
- `!command` syntax for password-manager integration (opt-in, not default)
- Credential redaction in `String()` / `GoString()` (logging only)

## Target Architecture

Use [Mozilla SOPS](https://github.com/getsops/sops) to encrypt secrets at rest
via the `go.mozilla.org/sops/v3` library. SOPS encrypts values while leaving
keys in plaintext, so the file structure remains inspectable and diffable.

**auth.json format (after migration):**
```json
{
  "anthropic": {
    "type": "ENC[AES256_GCM,data:...,tag:...]",
    "key": "ENC[AES256_GCM,data:...,tag:...]"
  },
  "sops": {
    "age": [{ "recipient": "age1...", "enc": "..." }],
    "lastmodified": "2026-03-28T...",
    "version": "3.9.0"
  }
}
```

**Key management (age as default):**
- [age](https://github.com/FiloSottile/age) is the default backend — no cloud
  account, no GPG keyring, single static file
- Key stored at `~/.gi/age-key.txt` (0600), gitignored
- Cloud KMS supported for teams: AWS KMS, GCP KMS, Azure Key Vault

## Implementation Plan

### Phase 1: age key bootstrapping + SOPS decrypt on load

**Goal:** `Store.Load()` transparently decrypts SOPS-encrypted auth.json.
Unencrypted files continue to work (backward compat).

**Changes:**

1. **Add dependencies**
   - `go.mozilla.org/sops/v3` (SOPS library)
   - `filippo.io/age` (age encryption)

2. **New file: `pkg/auth/sops.go`**
   - `isSopsEncrypted(data []byte) bool` — detect SOPS metadata in JSON
   - `decryptSops(data []byte) ([]byte, error)` — decrypt using age key
   - `encryptSops(data []byte, agePubKey string) ([]byte, error)` — encrypt
   - `LoadAgeKey(path string) (*age.X25519Identity, error)` — read key file
   - `GenerateAgeKey(path string) (*age.X25519Identity, error)` — create key

3. **Modify `Store` struct**
   - Add `sopsKey string` field (age public key, set if encryption enabled)
   - Add `encrypted bool` field (whether loaded file was SOPS-encrypted)

4. **Modify `Store.Load()`**
   - After reading raw bytes, check `isSopsEncrypted(data)`
   - If true: decrypt before JSON unmarshal, set `s.encrypted = true`
   - If false: proceed as today (backward compat)

5. **Modify `Store.Save()`**
   - If `s.sopsKey != ""`: encrypt after JSON marshal, before write
   - If `s.sopsKey == ""` and `s.encrypted`: warn (was encrypted, saving plain)

6. **New file: `pkg/auth/sops_test.go`**
   - Round-trip: generate key → encrypt → decrypt → compare
   - Backward compat: unencrypted file loads without error
   - Mixed: SOPS file loads correctly, saves re-encrypted

### Phase 2: `gi auth encrypt` / `gi auth decrypt` commands

**Goal:** User-facing commands to enable/disable encryption.

**Commands:**

```
gi auth encrypt          # Encrypt auth.json in place (generates age key if needed)
gi auth decrypt          # Decrypt auth.json back to plaintext
gi auth sops-status      # Show encryption status + key info
gi auth export-age-key   # Print public key (for sharing with team KMS policies)
```

**Changes:**

1. **New file: `pkg/cmd/auth_encrypt.go`**
   - `gi auth encrypt`:
     - Check if `~/.gi/age-key.txt` exists; if not, generate + print public key
     - Load auth.json → encrypt with SOPS → write back
     - Update `.gi/sops-config.json` with `{ "enabled": true, "age_key": "~/.gi/age-key.txt" }`
   - `gi auth decrypt`:
     - Load + decrypt → write plaintext → remove sops-config marker
   - `gi auth sops-status`:
     - Report: encrypted? key file? key age? backend?

2. **New file: `pkg/auth/sops_config.go`**
   - `SopsConfig` struct: `Enabled bool`, `AgeKeyPath string`, `KMSArn string`, etc.
   - Stored at `~/.gi/sops-config.json`
   - `Store.NewStore()` reads this on init to set `sopsKey`

### Phase 3: MCP server config encryption

**Goal:** Encrypt secret-bearing fields in settings.json MCP server definitions.

SOPS supports per-key encryption rules via `.sops.yaml`. Use this to encrypt
only `env` and `headers` fields in MCP server configs while leaving the rest
readable.

**Changes:**

1. **Create `.sops.yaml` template** (installed to `~/.gi/.sops.yaml`):
   ```yaml
   creation_rules:
     - path_regex: auth\.json$
       age: >-
         age1...
     - path_regex: settings\.json$
       encrypted_regex: ^(env|headers)$
       age: >-
         age1...
   ```

2. **Modify `config.LoadConfig()`**
   - Detect SOPS-encrypted settings.json → decrypt before merge
   - Only decrypt fields matching the encrypted_regex

3. **Add `gi config encrypt` / `gi config decrypt`** (parallel to auth commands)

### Phase 4: Migration UX + auto-encrypt on first save

**Goal:** Smooth migration path. New installs get encryption by default.

1. **First-run flow** (in `gi auth login` or `gi init`):
   - "Would you like to encrypt stored credentials? (recommended) [Y/n]"
   - If yes: generate age key, enable SOPS
   - Print: "Age key saved to ~/.gi/age-key.txt — back this up!"

2. **Existing user migration**:
   - On `Store.Load()` of unencrypted file: log warning once per session
   - `gi auth encrypt` handles in-place migration

3. **Backup key UX**:
   - `gi auth encrypt` prints recovery instructions
   - `gi auth export-age-key` for scripting

4. **Update SECURITY.md**:
   - Document SOPS encryption as default recommendation
   - Remove "0644" reference (it's already 0600 in code)
   - Add age key backup instructions

## File Change Summary

| File | Action | Description |
|------|--------|-------------|
| `go.mod` | modify | Add sops + age dependencies |
| `pkg/auth/sops.go` | new | SOPS encrypt/decrypt, age key management |
| `pkg/auth/sops_config.go` | new | SOPS configuration (enabled, key path, KMS) |
| `pkg/auth/sops_test.go` | new | Encryption round-trip + backward compat tests |
| `pkg/auth/store.go` | modify | Integrate SOPS decrypt in Load, encrypt in Save |
| `pkg/cmd/auth_encrypt.go` | new | `gi auth encrypt/decrypt/sops-status` commands |
| `pkg/config/config.go` | modify | SOPS-aware settings.json loading (Phase 3) |
| `SECURITY.md` | modify | Document encryption, update recommendations |
| `.gitignore` | modify | Add `age-key.txt` pattern |

## Sequencing and Dependencies

```
Phase 1 (core)         Phase 2 (CLI)          Phase 3 (config)       Phase 4 (UX)
─────────────────  →  ──────────────────  →  ──────────────────  →  ─────────────────
sops.go + store.go     auth encrypt cmd       .sops.yaml template    first-run prompt
age key bootstrap      auth decrypt cmd       config.go changes      migration warning
backward compat        sops-status cmd        config encrypt cmd     SECURITY.md update
tests                  sops_config.go         tests                  backup key UX
```

Phases 1-2 are the critical path. Phase 3 is important but lower urgency (MCP
config secrets are less common than auth.json credentials). Phase 4 is polish.

## Design Decisions

**Why age over GPG?**
- Single file, no keyring, no agent, no expiry headaches
- First-class SOPS support
- Easy to generate programmatically
- GPG is supported as an alternative but not the default

**Why SOPS over custom encryption?**
- Battle-tested (Mozilla, widely adopted in GitOps)
- Supports multiple key backends (age, PGP, cloud KMS)
- Encrypts values, keeps keys readable — easier debugging
- Go library available (`go.mozilla.org/sops/v3`)
- Users who already use SOPS in their infra get a consistent experience

**Why not just rely on `!command`?**
- `!command` is opt-in and requires a password manager
- SOPS encrypts the default storage path — no user action needed after setup
- `!command` remains supported as an alternative for dynamic retrieval

**Why encrypt values, not the whole file?**
- SOPS value-level encryption keeps JSON keys visible for debugging
- `type` field visibility lets tooling detect credential type without decrypting
- Diff-friendly: only changed values produce different ciphertext

**Backward compatibility:**
- Unencrypted auth.json continues to work indefinitely
- No forced migration — `gi auth encrypt` is opt-in (until Phase 4 default)
- Phase 4 makes encryption the default for NEW installs only

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| User loses age key | Permanent credential lockout | Prominent backup warning; `gi auth decrypt` before key rotation; document recovery |
| SOPS library bugs | Can't load credentials | Pin SOPS version; keep backward compat for plaintext |
| Windows age key permissions | Key readable by other users | Use Windows DPAPI or ACLs for key file (Phase 1 addresses in `sops_windows.go`) |
| Performance overhead | Slower startup | SOPS decrypt is ~2ms for small files; negligible |
| `!command` + SOPS interaction | Double-encrypted or broken resolution | `!command` values are encrypted at rest by SOPS, resolved at runtime by KeyResolver — no conflict |

## Open Questions

1. **Should `gi auth encrypt` require confirmation?** Probably yes for decrypt
   (removes protection), no for encrypt (strictly safer).
2. **Team key sharing:** Should we support a `.sops.yaml` in the project root for
   team-shared MCP secrets? Useful but out of scope for v1.
3. **Key rotation:** SOPS supports re-encrypting with new keys. Add
   `gi auth rotate-key` in a follow-up?
