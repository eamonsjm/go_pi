package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ejm/go_pi/pkg/auth"
	"github.com/ejm/go_pi/pkg/config"
)

// openBrowser attempts to open the given URL in the user's default browser.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default: // linux, freebsd, etc.
		return exec.Command("xdg-open", url).Start()
	}
}

// authLoginSuccessMsg is sent after a successful login. The app handles this
// by re-resolving the provider and wiring it into the agent loop, so that
// the user can immediately start chatting after /login.
type authLoginSuccessMsg struct {
	providerName string
	text         string
}

// authOAuthMsg is sent when the authorization URL is ready. The app shows
// the URL and instructions, then enters "auth pending" mode. The user's
// next input is treated as the authorization code and sent to codeCh.
// The waitCmd blocks on codeCh, exchanges the code for tokens, and returns
// a CommandResultMsg.
type authOAuthMsg struct {
	providerName string
	url          string
	codeCh       chan string
	waitCmd      tea.Cmd
	cancelAuth   context.CancelFunc // cancels the Login goroutine on TUI exit
}

// NewLoginCommand creates the /login slash command for OAuth browser login.
func NewLoginCommand(store *auth.Store, resolver *auth.Resolver) *SlashCommand {
	return &SlashCommand{
		Name:        "login",
		Description: "Log in via OAuth (e.g. /login anthropic, /login openai)",
		Execute: func(args string) tea.Cmd {
			provider := strings.TrimSpace(args)
			if provider == "" {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    "Usage: /login <provider>\nAvailable: anthropic, openai",
						IsError: true,
					}
				}
			}

			oauthProv := resolver.GetOAuthProvider(provider)
			if oauthProv == nil {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    fmt.Sprintf("No OAuth support for %q. Set API key via env var or ~/.gi/auth.json instead.", provider),
						IsError: true,
					}
				}
			}

			return func() tea.Msg {
				if anthProv, ok := oauthProv.(*auth.AnthropicOAuth); ok {
					return loginCodePasteFlow(anthProv, oauthProv, provider, store)
				}
				return loginCallbackFlow(oauthProv, provider, store)
			}
		},
	}
}

// loginCallbackFlow handles OAuth for providers that use a local callback
// server (e.g. OpenAI). It starts Login in a background goroutine and waits
// for the authorize URL via OnAuth, then returns an authOAuthMsg whose
// waitCmd blocks until the callback completes.
func loginCallbackFlow(oauthProv auth.OAuthProvider, provider string, store *auth.Store) tea.Msg {
	type loginResult struct {
		cred *auth.Credential
		err  error
	}
	ctx, cancel := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	resultCh := make(chan loginResult, 1)

	go func() {
		cred, err := oauthProv.Login(ctx, auth.OAuthCallbacks{
			OnAuth: func(url, _ string) {
				urlCh <- url
			},
		})
		resultCh <- loginResult{cred: cred, err: err}
	}()

	saveAndReturn := func(cred *auth.Credential) tea.Msg {
		store.Set(provider, cred)
		if err := store.Save(); err != nil {
			return CommandResultMsg{
				Text:    fmt.Sprintf("Login succeeded but failed to save: %v", err),
				IsError: true,
			}
		}
		return authLoginSuccessMsg{
			providerName: provider,
			text:         fmt.Sprintf("Logged in to %s!", oauthProv.Name()),
		}
	}

	select {
	case authURL := <-urlCh:
		return authOAuthMsg{
			providerName: oauthProv.Name(),
			url:          authURL,
			codeCh:       nil,
			cancelAuth:   cancel,
			waitCmd: func() tea.Msg {
				defer cancel()
				result := <-resultCh
				if result.err != nil {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Login failed: %v", result.err),
						IsError: true,
					}
				}
				return saveAndReturn(result.cred)
			},
		}
	case result := <-resultCh:
		cancel()
		if result.err != nil {
			return CommandResultMsg{
				Text:    fmt.Sprintf("Login failed: %v", result.err),
				IsError: true,
			}
		}
		return saveAndReturn(result.cred)
	}
}

// loginCodePasteFlow handles Anthropic's code-paste OAuth flow. It starts
// the PKCE auth flow to get an authorize URL, then returns an authOAuthMsg
// that prompts the user to paste the authorization code.
func loginCodePasteFlow(anthProv *auth.AnthropicOAuth, oauthProv auth.OAuthProvider, provider string, store *auth.Store) tea.Msg {
	session, err := anthProv.StartAuthFlow()
	if err != nil {
		return CommandResultMsg{
			Text:    fmt.Sprintf("Login failed: %v", err),
			IsError: true,
		}
	}

	codeCh := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())

	return authOAuthMsg{
		providerName: oauthProv.Name(),
		url:          session.AuthorizeURL,
		codeCh:       codeCh,
		cancelAuth:   cancel,
		waitCmd: func() tea.Msg {
			defer cancel()
			var code string
			select {
			case code = <-codeCh:
			case <-ctx.Done():
				return CommandResultMsg{
					Text:    "Login cancelled.",
					IsError: true,
				}
			case <-time.After(defaultLoginTimeout):
				return CommandResultMsg{
					Text:    "Login timed out — no code entered within 10 minutes.",
					IsError: true,
				}
			}

			code = strings.TrimSpace(code)
			if code == "" {
				return CommandResultMsg{
					Text:    "Login cancelled — empty code.",
					IsError: true,
				}
			}

			exchCtx, exchCancel := context.WithTimeout(ctx, 30*time.Second)
			defer exchCancel()
			cred, err := anthProv.ExchangeCode(exchCtx, session, code)
			if err != nil {
				return CommandResultMsg{
					Text:    fmt.Sprintf("Login failed: %v", err),
					IsError: true,
				}
			}
			store.Set(provider, cred)
			if err := store.Save(); err != nil {
				return CommandResultMsg{
					Text:    fmt.Sprintf("Login succeeded but failed to save: %v", err),
					IsError: true,
				}
			}
			return authLoginSuccessMsg{
				providerName: provider,
				text:         fmt.Sprintf("Successfully logged in to %s!", oauthProv.Name()),
			}
		},
	}
}

const defaultLoginTimeout = 10 * time.Minute

// NewLogoutCommand creates the /logout slash command.
func NewLogoutCommand(store *auth.Store) *SlashCommand {
	return &SlashCommand{
		Name:        "logout",
		Description: "Log out from a provider (e.g. /logout anthropic)",
		Execute: func(args string) tea.Cmd {
			provider := strings.TrimSpace(args)
			if provider == "" {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    "Usage: /logout <provider>",
						IsError: true,
					}
				}
			}

			return func() tea.Msg {
				store.Delete(provider)
				if err := store.Save(); err != nil {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Failed to save after logout: %v", err),
						IsError: true,
					}
				}
				return CommandResultMsg{
					Text: fmt.Sprintf("Logged out from %s. Credentials removed.", provider),
				}
			}
		},
	}
}

// NewAuthStatusCommand creates the /auth slash command that shows auth status.
func NewAuthStatusCommand(ctx context.Context, store *auth.Store, resolver *auth.Resolver) *SlashCommand {
	return &SlashCommand{
		Name:        "auth",
		Description: "Show authentication status for all providers",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				var sb strings.Builder
				sb.WriteString("Authentication Status:\n")

				for _, p := range config.ValidProviderNames() {
					key, _ := resolver.Resolve(ctx, p)
					cred := store.Get(p)

					if key != "" {
						fmt.Fprintf(&sb, "\n  %s: authenticated", p)
						if cred != nil {
							switch cred.Type {
							case auth.CredentialOAuth:
								if cred.IsExpired() {
									sb.WriteString(" (OAuth, token expired)")
								} else if cred.ExpiresAt > 0 {
									expires := time.UnixMilli(cred.ExpiresAt)
									fmt.Fprintf(&sb, " (OAuth, expires %s)",
										expires.Format("Jan 2 15:04"))
								} else {
									sb.WriteString(" (OAuth)")
								}
							case auth.CredentialAPIKey:
								sb.WriteString(" (API key)")
							}
						} else {
							sb.WriteString(" (env var)")
						}
					} else if envName, ok := config.ProviderConfigEnvVar(p); ok {
						if val := os.Getenv(envName); val != "" {
							fmt.Fprintf(&sb, "\n  %s: configured (%s set)", p, envName)
						} else {
							fmt.Fprintf(&sb, "\n  %s: not configured", p)
						}
					} else {
						fmt.Fprintf(&sb, "\n  %s: not configured", p)
					}
				}
				sb.WriteString("\n")

				return CommandResultMsg{Text: sb.String()}
			}
		},
	}
}
