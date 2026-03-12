package auth

import (
	"fmt"
	"os"

	"github.com/ejm/go_pi/pkg/config"
)

// Resolver resolves an API key for a provider by walking a priority chain:
//
//  1. CLI flag / explicit override
//  2. Stored API key credentials (from Store)
//  3. OAuth tokens (with auto-refresh if expired)
//  4. Environment variables
//  5. Custom provider-specific resolution
//
// Each step is tried in order; the first non-empty result wins.
type Resolver struct {
	store     *Store
	providers map[string]OAuthProvider // registered OAuth providers
	overrides map[string]string        // CLI flag overrides (provider → key)
}

// NewResolver creates a Resolver backed by the given credential store.
func NewResolver(store *Store) *Resolver {
	return &Resolver{
		store:     store,
		providers: make(map[string]OAuthProvider),
		overrides: make(map[string]string),
	}
}

// RegisterProvider registers an OAuth provider for token refresh during resolution.
func (r *Resolver) RegisterProvider(p OAuthProvider) {
	r.providers[p.ID()] = p
}

// SetOverride sets a CLI-flag override for a provider's API key.
// Overrides take highest precedence and bypass all other sources.
func (r *Resolver) SetOverride(provider, key string) {
	r.overrides[provider] = key
}

// GetOAuthProvider returns the registered OAuth provider for the given ID,
// or nil if none is registered.
func (r *Resolver) GetOAuthProvider(id string) OAuthProvider {
	return r.providers[id]
}

// Resolve returns a usable API key for the given provider by walking
// the resolution chain. Returns an error only if resolution fails at every
// level; an empty key with nil error means no credentials are configured.
func (r *Resolver) Resolve(provider string) (string, error) {
	// 1. CLI override.
	if key, ok := r.overrides[provider]; ok && key != "" {
		return key, nil
	}

	// 2-3. Stored credential (API key or OAuth).
	if cred := r.store.Get(provider); cred != nil {
		key, err := r.resolveCredential(provider, cred)
		if err == nil && key != "" {
			return key, nil
		}
		// Fall through on error — try env vars.
	}

	// 4. Environment variable.
	if envName, ok := config.ProviderEnvVars[provider]; ok {
		if val := os.Getenv(envName); val != "" {
			return val, nil
		}
	}

	// 5. Nothing found.
	return "", nil
}

// resolveCredential handles stored credential types.
func (r *Resolver) resolveCredential(provider string, cred *Credential) (string, error) {
	switch cred.Type {
	case CredentialAPIKey, CredentialHeader:
		return cred.ResolveKey()

	case CredentialOAuth:
		return r.resolveOAuth(provider, cred)

	default:
		return "", fmt.Errorf("unknown credential type %q for %s", cred.Type, provider)
	}
}

// resolveOAuth handles OAuth credentials, refreshing if expired.
func (r *Resolver) resolveOAuth(provider string, cred *Credential) (string, error) {
	if !cred.IsExpired() {
		p, ok := r.providers[provider]
		if ok {
			return p.GetAPIKey(cred), nil
		}
		return cred.AccessToken, nil
	}

	// Token expired — try refresh.
	p, ok := r.providers[provider]
	if !ok {
		return "", fmt.Errorf("provider %q: OAuth token expired and no provider registered for refresh", provider)
	}

	if err := r.store.Lock(); err != nil {
		return "", fmt.Errorf("token refresh lock: %w", err)
	}
	defer r.store.Unlock()

	// Re-read store inside lock — another process may have refreshed already.
	if err := r.store.Load(); err != nil {
		return "", fmt.Errorf("token refresh load: %w", err)
	}
	fresh := r.store.Get(provider)
	if fresh != nil && !fresh.IsExpired() {
		*cred = *fresh // update caller's view
		return p.GetAPIKey(cred), nil
	}

	// Still expired — do the refresh.
	refreshed, err := p.RefreshToken(cred)
	if err != nil {
		return "", fmt.Errorf("token refresh: %w", err)
	}

	r.store.Set(provider, refreshed)
	if err := r.store.Save(); err != nil {
		return "", fmt.Errorf("save refreshed token: %w", err)
	}
	*cred = *refreshed
	return p.GetAPIKey(cred), nil
}
