package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
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
				anthProv, ok := oauthProv.(*auth.AnthropicOAuth)
				if !ok {
					// Non-Anthropic provider (e.g. OpenAI): uses a local callback
					// server. Capture the authorize URL via OnAuth so the TUI can
					// display it and open the browser. Login() runs in a background
					// goroutine; the waitCmd blocks until the callback completes.
					//
					// Use a cancellable context so the Login goroutine (which may
					// hold an HTTP server or browser process) is cleaned up if the
					// TUI exits before Login completes. Channels are buffered so
					// the goroutine never blocks on sends.
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

					// Wait for either the URL (OnAuth fired) or an early failure.
					select {
					case authURL := <-urlCh:
						return authOAuthMsg{
							providerName: oauthProv.Name(),
							url:          authURL,
							codeCh:       nil, // callback-based: no code paste needed
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
								store.Set(provider, result.cred)
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
							},
						}
					case result := <-resultCh:
						// Login completed or failed before OnAuth was called.
						cancel()
						if result.err != nil {
							return CommandResultMsg{
								Text:    fmt.Sprintf("Login failed: %v", result.err),
								IsError: true,
							}
						}
						store.Set(provider, result.cred)
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
				}

				// Anthropic: code-paste flow. Start the auth flow (generates
				// PKCE + authorize URL), then let the TUI prompt for the code.
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
						// Block until the user pastes the code.
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
func NewAuthStatusCommand(store *auth.Store, resolver *auth.Resolver) *SlashCommand {
	return &SlashCommand{
		Name:        "auth",
		Description: "Show authentication status for all providers",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				var sb strings.Builder
				sb.WriteString("Authentication Status:\n")

				providers := make([]string, 0, len(config.ProviderEnvVars))
				for k := range config.ProviderEnvVars {
					providers = append(providers, k)
				}
				sort.Strings(providers)
				for _, p := range providers {
					key, _ := resolver.Resolve(context.TODO(), p)
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
