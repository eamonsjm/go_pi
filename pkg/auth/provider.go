package auth

// OAuthProvider defines the interface that each OAuth-capable provider must
// implement. Provider-specific implementations (Anthropic, GitHub Copilot, etc.)
// live in separate packages or files.
type OAuthProvider interface {
	// ID returns a stable identifier for this provider (e.g. "anthropic", "github-copilot").
	ID() string

	// Name returns a human-readable display name.
	Name() string

	// Login performs the full OAuth login flow, using callbacks to interact
	// with the user (display URLs, prompt for input, show progress).
	// Returns the resulting credentials on success.
	Login(callbacks OAuthCallbacks) (*Credential, error)

	// RefreshToken exchanges a refresh token for new access + refresh tokens.
	// The input credential must have Type == CredentialOAuth with a valid RefreshToken.
	RefreshToken(cred *Credential) (*Credential, error)

	// GetAPIKey extracts a usable API key string from OAuth credentials.
	// Some providers issue API keys via OAuth; others use the access token directly.
	GetAPIKey(cred *Credential) string
}

// OAuthCallbacks allows the OAuth flow to interact with the calling environment
// (TUI, CLI, headless, etc.) without hard-coding UI concerns.
type OAuthCallbacks struct {
	// OnAuth is called when the user needs to visit a URL to authenticate.
	// instructions contains provider-specific guidance (e.g. "paste the code below").
	OnAuth func(url, instructions string)

	// OnPrompt asks the user for text input (e.g. a manually pasted auth code).
	// Returns the user's response.
	OnPrompt func(prompt string) (string, error)

	// OnProgress reports a status message during the login flow.
	OnProgress func(message string)
}
