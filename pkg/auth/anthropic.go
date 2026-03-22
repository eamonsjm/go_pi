package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAnthropicAuthorizeURL = "https://claude.ai"
	defaultAnthropicTokenURL     = "https://console.anthropic.com"
	defaultAnthropicClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	defaultAnthropicRedirectURI  = "https://console.anthropic.com/oauth/code/callback"
	defaultAnthropicScope        = "org:create_api_key user:profile user:inference"
	defaultAuthTimeout           = 5 * time.Minute
)

// AnthropicOAuth implements OAuthProvider for Anthropic using
// OAuth 2.0 authorization code grant with PKCE (RFC 7636).
//
// The flow uses Anthropic's hosted callback page: the user authorizes in
// the browser, gets redirected to console.anthropic.com which displays the
// authorization code, and then pastes the code back into the TUI.
type AnthropicOAuth struct {
	AuthorizeURL string // Base URL for /oauth/authorize (default: https://claude.ai)
	TokenURL     string // Base URL for /v1/oauth/token (default: https://console.anthropic.com)
	ClientID     string
	RedirectURI  string
	Scope        string
	HTTPClient   *http.Client
}

// NewAnthropicOAuth creates an Anthropic OAuth provider with default settings.
func NewAnthropicOAuth() *AnthropicOAuth {
	return &AnthropicOAuth{
		AuthorizeURL: defaultAnthropicAuthorizeURL,
		TokenURL:     defaultAnthropicTokenURL,
		ClientID:     defaultAnthropicClientID,
		RedirectURI:  defaultAnthropicRedirectURI,
		Scope:        defaultAnthropicScope,
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *AnthropicOAuth) ID() string   { return "anthropic" }
func (a *AnthropicOAuth) Name() string { return "Anthropic (Claude)" }

// AuthSession holds state for an in-progress authorization code flow.
// Unlike a local-callback approach, the user manually pastes the code
// from Anthropic's redirect page.
type AuthSession struct {
	AuthorizeURL string
	PKCE         *PKCEChallenge
	State        string // Random state parameter for CSRF protection (distinct from PKCE verifier).
	RedirectURI  string
}

// tokenResponse is the JSON response from the token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	APIKey       string `json:"api_key,omitempty"`
}

// tokenExchangeRequest is the JSON body sent to the token endpoint.
type tokenExchangeRequest struct {
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	Code         string `json:"code,omitempty"`
	State        string `json:"state,omitempty"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	CodeVerifier string `json:"code_verifier,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// tokenErrorResponse is the error response from the token endpoint.
type tokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// StartAuthFlow generates PKCE parameters and builds the authorization URL.
// The caller should display the URL to the user and collect the authorization
// code they paste back from the redirect page.
func (a *AnthropicOAuth) StartAuthFlow() (*AuthSession, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generate PKCE: %w", err)
	}

	redirectURI := a.RedirectURI
	if redirectURI == "" {
		redirectURI = defaultAnthropicRedirectURI
	}

	// Generate a separate random state parameter for CSRF protection.
	// The PKCE verifier must NOT be used as the state — doing so exposes
	// the verifier in the authorization URL (browser history, referrer
	// headers, server logs), defeating PKCE's security guarantees.
	stateBuf := make([]byte, 32)
	if _, err := rand.Read(stateBuf); err != nil {
		return nil, fmt.Errorf("generate OAuth state: %w", err)
	}
	oauthState := base64URLEncode(stateBuf)

	// Build query string manually to preserve parameter order matching
	// Anthropic's expected format. url.Values.Encode() sorts alphabetically,
	// but Anthropic's OAuth server expects the order from the reference
	// implementation (URLSearchParams insertion order).
	authorizeURL := a.AuthorizeURL + "/oauth/authorize?" +
		"code=true" +
		"&client_id=" + url.QueryEscape(a.ClientID) +
		"&response_type=code" +
		"&redirect_uri=" + url.QueryEscape(redirectURI) +
		"&scope=" + url.QueryEscape(a.Scope) +
		"&code_challenge=" + url.QueryEscape(pkce.Challenge) +
		"&code_challenge_method=S256" +
		"&state=" + url.QueryEscape(oauthState)

	return &AuthSession{
		AuthorizeURL: authorizeURL,
		PKCE:         pkce,
		State:        oauthState,
		RedirectURI:  redirectURI,
	}, nil
}

// ExchangeCode exchanges the user-provided authorization code for tokens.
// The rawCode may be in "code#state" format from Anthropic's redirect page;
// if so, the state is extracted and used in the token exchange request.
func (a *AnthropicOAuth) ExchangeCode(ctx context.Context, session *AuthSession, rawCode string) (*Credential, error) {
	code := rawCode
	state := session.State
	if parts := strings.SplitN(rawCode, "#", 2); len(parts) == 2 {
		code = parts[0]
		state = parts[1]
	}

	reqBody := tokenExchangeRequest{
		GrantType:    "authorization_code",
		ClientID:     a.ClientID,
		Code:         code,
		State:        state,
		RedirectURI:  session.RedirectURI,
		CodeVerifier: session.PKCE.Verifier,
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.tokenEndpoint(), bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var detail string
		var errResp tokenErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			detail = errResp.ErrorDescription
			if detail == "" {
				detail = errResp.Error
			}
		}
		if detail == "" {
			detail = strings.TrimSpace(string(body))
		}
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}
		return nil, &TokenExchangeError{Operation: "token exchange", StatusCode: resp.StatusCode, Detail: detail}
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	return a.tokenToCredential(&token), nil
}

// Login implements OAuthProvider.Login by running the full authorization code
// + PKCE flow, using callbacks for user interaction.
func (a *AnthropicOAuth) Login(ctx context.Context, cb OAuthCallbacks) (*Credential, error) {
	session, err := a.StartAuthFlow()
	if err != nil {
		return nil, err
	}

	if cb.OnAuth != nil {
		cb.OnAuth(session.AuthorizeURL,
			"Open this URL in your browser to authorize, then paste the code below")
	}
	if cb.OnProgress != nil {
		cb.OnProgress("Waiting for authorization code...")
	}

	if cb.OnPrompt == nil {
		return nil, fmt.Errorf("Login requires an OnPrompt callback to receive the authorization code")
	}

	code, err := cb.OnPrompt("Paste authorization code")
	if err != nil {
		return nil, fmt.Errorf("prompt for code: %w", err)
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("empty authorization code")
	}

	cred, err := a.ExchangeCode(ctx, session, code)
	if err != nil {
		return nil, err
	}

	if cb.OnProgress != nil {
		cb.OnProgress("Authorization complete!")
	}
	return cred, nil
}

// RefreshToken implements OAuthProvider.RefreshToken.
func (a *AnthropicOAuth) RefreshToken(ctx context.Context, cred *Credential) (*Credential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	reqBody := tokenExchangeRequest{
		GrantType:    "refresh_token",
		RefreshToken: cred.RefreshToken,
		ClientID:     a.ClientID,
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal refresh request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.tokenEndpoint(), bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read refresh response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var detail string
		var errResp tokenErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			detail = errResp.ErrorDescription
			if detail == "" {
				detail = errResp.Error
			}
		}
		if detail == "" {
			detail = strings.TrimSpace(string(body))
		}
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}
		return nil, &TokenExchangeError{Operation: "refresh", StatusCode: resp.StatusCode, Detail: detail}
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	refreshed := a.tokenToCredential(&token)
	// Keep old refresh token if server didn't issue a new one.
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = cred.RefreshToken
	}
	return refreshed, nil
}

// GetAPIKey implements OAuthProvider.GetAPIKey.
func (a *AnthropicOAuth) GetAPIKey(cred *Credential) string {
	if cred.Key != "" {
		return cred.Key
	}
	return cred.AccessToken
}

// tokenEndpoint returns the token endpoint URL.
func (a *AnthropicOAuth) tokenEndpoint() string {
	return a.TokenURL + "/v1/oauth/token"
}

// tokenToCredential converts a token response to a Credential.
func (a *AnthropicOAuth) tokenToCredential(token *tokenResponse) *Credential {
	cred := &Credential{
		Type:         CredentialOAuth,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UnixMilli(),
	}
	if token.APIKey != "" {
		cred.Key = token.APIKey
	}
	return cred
}
