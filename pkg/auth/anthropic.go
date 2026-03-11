package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAnthropicAuthURL  = "https://console.anthropic.com"
	defaultAnthropicClientID = "pi-cli"
	defaultAnthropicScope    = "org:create_api_key user:profile user:inference"
	defaultAuthTimeout       = 5 * time.Minute
)

// AnthropicOAuth implements OAuthProvider for Anthropic using
// OAuth 2.0 authorization code grant with PKCE (RFC 7636).
type AnthropicOAuth struct {
	AuthURL    string
	ClientID   string
	Scope      string
	HTTPClient *http.Client
}

// NewAnthropicOAuth creates an Anthropic OAuth provider with default settings.
func NewAnthropicOAuth() *AnthropicOAuth {
	return &AnthropicOAuth{
		AuthURL:    defaultAnthropicAuthURL,
		ClientID:   defaultAnthropicClientID,
		Scope:      defaultAnthropicScope,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *AnthropicOAuth) ID() string   { return "anthropic" }
func (a *AnthropicOAuth) Name() string { return "Anthropic (Claude)" }

// AuthSession holds state for an in-progress authorization code flow.
type AuthSession struct {
	AuthorizeURL string
	PKCE         *PKCEChallenge
	State        string
	RedirectURI  string

	listener net.Listener
	codeCh   chan string
	errCh    chan error
	server   *http.Server
}

// Close shuts down the callback server and releases the port.
func (s *AuthSession) Close() {
	if s.server != nil {
		s.server.Shutdown(context.Background())
	}
}

// tokenResponse is the JSON response from the token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	APIKey       string `json:"api_key,omitempty"`
}

// tokenErrorResponse is the error response from the token endpoint.
type tokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// StartAuthFlow initiates the authorization code flow with PKCE.
// It starts a local HTTP server to receive the callback and returns an
// AuthSession containing the authorization URL to open in a browser.
func (a *AnthropicOAuth) StartAuthFlow() (*AuthSession, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generate PKCE: %w", err)
	}

	state, err := randomHex(16)
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	params := url.Values{
		"client_id":             {a.ClientID},
		"response_type":        {"code"},
		"redirect_uri":         {redirectURI},
		"scope":                {a.Scope},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
		"state":                {state},
	}

	authorizeURL := a.AuthURL + "/oauth/authorize?" + params.Encode()

	session := &AuthSession{
		AuthorizeURL: authorizeURL,
		PKCE:         pkce,
		State:        state,
		RedirectURI:  redirectURI,
		listener:     listener,
		codeCh:       make(chan string, 1),
		errCh:        make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			desc := r.URL.Query().Get("error_description")
			if desc == "" {
				desc = errMsg
			}
			http.Error(w, "Authorization failed. You can close this tab.", http.StatusBadRequest)
			session.errCh <- fmt.Errorf("authorization error: %s", desc)
			return
		}

		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid state parameter.", http.StatusBadRequest)
			session.errCh <- fmt.Errorf("state mismatch")
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code.", http.StatusBadRequest)
			session.errCh <- fmt.Errorf("missing authorization code in callback")
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h2>Authorization successful!</h2><p>You can close this tab and return to the terminal.</p></body></html>")
		session.codeCh <- code
	})

	session.server = &http.Server{Handler: mux}
	go session.server.Serve(listener)

	return session, nil
}

// ExchangeAuthCode waits for the browser callback and exchanges the
// authorization code for tokens. The session's callback server is shut
// down when this method returns.
func (a *AnthropicOAuth) ExchangeAuthCode(session *AuthSession, timeout time.Duration) (*Credential, error) {
	defer session.Close()

	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}

	var code string
	select {
	case code = <-session.codeCh:
	case err := <-session.errCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("authorization timed out — no response within %v", timeout)
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {a.ClientID},
		"redirect_uri":  {session.RedirectURI},
		"code_verifier": {session.PKCE.Verifier},
	}

	resp, err := a.HTTPClient.PostForm(a.tokenURL(), form)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (%d): %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	return a.tokenToCredential(&token), nil
}

// Login implements OAuthProvider.Login by running the full authorization code
// + PKCE flow, using callbacks for user interaction.
func (a *AnthropicOAuth) Login(cb OAuthCallbacks) (*Credential, error) {
	session, err := a.StartAuthFlow()
	if err != nil {
		return nil, err
	}

	if cb.OnAuth != nil {
		cb.OnAuth(session.AuthorizeURL,
			"Open this URL in your browser to authorize")
	}
	if cb.OnProgress != nil {
		cb.OnProgress("Waiting for authorization...")
	}

	cred, err := a.ExchangeAuthCode(session, defaultAuthTimeout)
	if err != nil {
		return nil, err
	}

	if cb.OnProgress != nil {
		cb.OnProgress("Authorization complete!")
	}
	return cred, nil
}

// RefreshToken implements OAuthProvider.RefreshToken.
func (a *AnthropicOAuth) RefreshToken(cred *Credential) (*Credential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {cred.RefreshToken},
		"client_id":     {a.ClientID},
	}

	resp, err := a.HTTPClient.PostForm(a.tokenURL(), form)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var errResp tokenErrorResponse
		json.Unmarshal(body, &errResp)
		return nil, fmt.Errorf("refresh failed (%d): %s",
			resp.StatusCode, errResp.ErrorDescription)
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

// tokenURL returns the token endpoint URL.
func (a *AnthropicOAuth) tokenURL() string {
	return a.AuthURL + "/v1/oauth/token"
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

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
