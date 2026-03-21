package auth

import (
	"context"
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
	defaultOpenAIAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	defaultOpenAITokenURL     = "https://auth.openai.com/oauth/token"
	defaultOpenAIClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultOpenAIRedirectURI  = "http://localhost:1455/auth/callback"
	defaultOpenAIScope        = "openid profile email offline_access"
)

// OpenAIOAuth implements OAuthProvider for OpenAI using OAuth 2.0
// authorization code grant with PKCE (RFC 7636).
//
// Unlike Anthropic's code-paste flow, OpenAI uses a local HTTP server
// callback: the user authorizes in the browser, gets redirected to
// localhost, and the server captures the authorization code automatically.
type OpenAIOAuth struct {
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	RedirectURI  string
	Scope        string
	HTTPClient   *http.Client
}

// NewOpenAIOAuth creates an OpenAI OAuth provider with default settings.
func NewOpenAIOAuth() *OpenAIOAuth {
	return &OpenAIOAuth{
		AuthorizeURL: defaultOpenAIAuthorizeURL,
		TokenURL:     defaultOpenAITokenURL,
		ClientID:     defaultOpenAIClientID,
		RedirectURI:  defaultOpenAIRedirectURI,
		Scope:        defaultOpenAIScope,
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (o *OpenAIOAuth) ID() string   { return "openai" }
func (o *OpenAIOAuth) Name() string { return "OpenAI (ChatGPT)" }

// Login implements OAuthProvider.Login by starting a local HTTP server,
// opening the browser for authorization, and waiting for the callback.
func (o *OpenAIOAuth) Login(ctx context.Context, cb OAuthCallbacks) (*Credential, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generate PKCE: %w", err)
	}

	redirectURL, err := url.Parse(o.RedirectURI)
	if err != nil {
		return nil, fmt.Errorf("parse redirect URI: %w", err)
	}

	// Start local HTTP server to receive the OAuth callback.
	listener, err := net.Listen("tcp", redirectURL.Host)
	if err != nil {
		return nil, fmt.Errorf("start callback server on %s: %w", redirectURL.Host, err)
	}
	defer func() { _ = listener.Close() }()

	// Build authorize URL.
	params := url.Values{}
	params.Set("client_id", o.ClientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", o.RedirectURI)
	params.Set("scope", o.Scope)
	params.Set("code_challenge", pkce.Challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", pkce.Verifier)
	authorizeURL := o.AuthorizeURL + "?" + params.Encode()

	if cb.OnAuth != nil {
		cb.OnAuth(authorizeURL, "Opening browser for OpenAI login...")
	}
	if cb.OnProgress != nil {
		cb.OnProgress("Waiting for browser login...")
	}

	// Wait for the callback with the authorization code.
	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)

	callbackPath := redirectURL.Path
	if callbackPath == "" {
		callbackPath = "/"
	}

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		errParam := r.URL.Query().Get("error")
		if errParam != "" {
			desc := r.URL.Query().Get("error_description")
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, "<html><body><h2>Login Failed</h2><p>%s</p></body></html>",
				strings.ReplaceAll(desc, "<", "&lt;"))
			resultCh <- callbackResult{err: fmt.Errorf("oauth error: %s: %s", errParam, desc)}
			return
		}

		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")

		if state != pkce.Verifier {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<html><body><h2>Login Failed</h2><p>State mismatch.</p></body></html>")
			resultCh <- callbackResult{err: fmt.Errorf("state mismatch in OAuth callback")}
			return
		}

		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><body><h2>Login Successful</h2>"+
			"<p>You can close this tab and return to the terminal.</p></body></html>")
		resultCh <- callbackResult{code: code}
	})

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		// Use a timeout so Shutdown doesn't block indefinitely if a
		// request handler is stuck.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	timeout := time.NewTimer(defaultAuthTimeout)
	defer timeout.Stop()

	select {
	case result := <-resultCh:
		if result.err != nil {
			return nil, result.err
		}
		cred, err := o.exchangeCode(ctx, result.code, pkce.Verifier)
		if err != nil {
			return nil, err
		}
		if cb.OnProgress != nil {
			cb.OnProgress("Authorization complete!")
		}
		return cred, nil

	case <-timeout.C:
		return nil, fmt.Errorf("login timed out — no callback received within %v", defaultAuthTimeout)
	}
}

// exchangeCode exchanges an authorization code for tokens using
// form-encoded POST to the token endpoint.
func (o *OpenAIOAuth) exchangeCode(ctx context.Context, code, codeVerifier string) (*Credential, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", o.ClientID)
	data.Set("code", code)
	data.Set("redirect_uri", o.RedirectURI)
	data.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, "POST", o.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (%d): %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	return o.tokenToCredential(&token), nil
}

// RefreshToken implements OAuthProvider.RefreshToken.
func (o *OpenAIOAuth) RefreshToken(ctx context.Context, cred *Credential) (*Credential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", o.ClientID)
	data.Set("refresh_token", cred.RefreshToken)

	req, err := http.NewRequestWithContext(ctx, "POST", o.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read refresh response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		detail := strings.TrimSpace(string(body))
		var errResp tokenErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil && errResp.ErrorDescription != "" {
			detail = errResp.ErrorDescription
		}
		return nil, fmt.Errorf("refresh failed (%d): %s",
			resp.StatusCode, detail)
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	refreshed := o.tokenToCredential(&token)
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = cred.RefreshToken
	}
	return refreshed, nil
}

// GetAPIKey implements OAuthProvider.GetAPIKey.
// OpenAI OAuth tokens are used directly as Bearer tokens.
func (o *OpenAIOAuth) GetAPIKey(cred *Credential) string {
	return cred.AccessToken
}

func (o *OpenAIOAuth) tokenToCredential(token *tokenResponse) *Credential {
	return &Credential{
		Type:         CredentialOAuth,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UnixMilli(),
	}
}
