package auth

import (
	"context"
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
	store       *Store
	providers   map[string]OAuthProvider // registered OAuth providers
	overrides   map[string]string        // CLI flag overrides (provider → key)
	keyResolver *KeyResolver             // resolves API key values (!commands, env vars)
}

// NewResolver creates a Resolver backed by the given credential store.
func NewResolver(store *Store) *Resolver {
	return &Resolver{
		store:       store,
		providers:   make(map[string]OAuthProvider),
		overrides:   make(map[string]string),
		keyResolver: NewKeyResolver(),
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

// IsOAuthToken reports whether the stored credential for the given provider
// is an OAuth token (not a real API key). When true, the caller should use
// Bearer auth instead of x-api-key.
func (r *Resolver) IsOAuthToken(provider string) bool {
	cred := r.store.Get(provider)
	return cred != nil && cred.Type == CredentialOAuth && cred.Key == ""
}

// Resolve returns a usable API key for the given provider by walking
// the resolution chain. Returns an error only if resolution fails at every
// level; an empty key with nil error means no credentials are configured.
func (r *Resolver) Resolve(ctx context.Context, provider string) (string, error) {
	// 1. CLI override.
	if key, ok := r.overrides[provider]; ok && key != "" {
		return key, nil
	}

	// 2-3. Stored credential (API key or OAuth).
	if cred := r.store.Get(provider); cred != nil {
		key, err := r.resolveCredential(ctx, provider, cred)
		if err == nil && key != "" {
			return key, nil
		}
		// For OAuth, surface the error — silently falling through masks
		// refresh failures and produces opaque API errors downstream.
		if err != nil && cred.Type == CredentialOAuth {
			return "", fmt.Errorf("%s: %w (use /login to re-authenticate)", provider, err)
		}
		// Fall through on error for non-OAuth — try env vars.
	}

	// 4. Environment variable (API key credentials only).
	if envName, ok := config.ProviderAPIKeyEnvVar(provider); ok {
		if val := os.Getenv(envName); val != "" {
			return val, nil
		}
	}

	// 5. Nothing found.
	return "", nil
}

// resolveCredential handles stored credential types.
func (r *Resolver) resolveCredential(ctx context.Context, provider string, cred *Credential) (string, error) {
	switch cred.Type {
	case CredentialAPIKey:
		return r.keyResolver.ResolveKeyValue(cred.Key)

	case CredentialOAuth:
		return r.resolveOAuth(ctx, provider, cred)

	default:
		return "", fmt.Errorf("unknown credential type %q for %s", cred.Type, provider)
	}
}

// resolveOAuth handles OAuth credentials, refreshing if expired.
func (r *Resolver) resolveOAuth(ctx context.Context, provider string, cred *Credential) (string, error) {
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

	var key string
	if err := r.store.WithLock(func() error {
		// Re-read store inside lock — another process may have refreshed already.
		if err := r.store.Load(); err != nil {
			return fmt.Errorf("token refresh load: %w", err)
		}
		fresh := r.store.Get(provider)
		if fresh != nil && !fresh.IsExpired() {
			*cred = *fresh // update caller's view
			key = p.GetAPIKey(cred)
			return nil
		}

		// Still expired — do the refresh.
		refreshed, err := p.RefreshToken(ctx, cred)
		if err != nil {
			return fmt.Errorf("token refresh: %w", err)
		}

		r.store.Set(provider, refreshed)
		if err := r.store.Save(); err != nil {
			return fmt.Errorf("save refreshed token: %w", err)
		}
		*cred = *refreshed
		key = p.GetAPIKey(cred)
		return nil
	}); err != nil {
		return "", fmt.Errorf("token refresh lock: %w", err)
	}
	return key, nil
}
