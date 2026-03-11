package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ejm/go_pi/pkg/auth"
)

// authOAuthMsg is sent when the authorization URL is ready (Phase 1).
// The handler shows the URL to the user, then starts waiting for the
// browser callback (Phase 2).
type authOAuthMsg struct {
	providerName string
	url          string
	waitCmd      tea.Cmd
}

// NewLoginCommand creates the /login slash command for OAuth browser login.
func NewLoginCommand(store *auth.Store, resolver *auth.Resolver) *SlashCommand {
	return &SlashCommand{
		Name:        "login",
		Description: "Log in to a provider via OAuth (e.g. /login anthropic)",
		Execute: func(args string) tea.Cmd {
			provider := strings.TrimSpace(args)
			if provider == "" {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    "Usage: /login <provider>\nAvailable: anthropic",
						IsError: true,
					}
				}
			}

			oauthProv := resolver.GetOAuthProvider(provider)
			if oauthProv == nil {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    fmt.Sprintf("No OAuth support for %q. Set API key via env var or ~/.pi/auth.json instead.", provider),
						IsError: true,
					}
				}
			}

			// Phase 1: Start auth flow (opens local callback server).
			// Returns authOAuthMsg which triggers Phase 2 in app.Update.
			return func() tea.Msg {
				anthProv, ok := oauthProv.(*auth.AnthropicOAuth)
				if !ok {
					// Non-browser provider: run full Login flow in one shot.
					cred, err := oauthProv.Login(auth.OAuthCallbacks{})
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
					return CommandResultMsg{
						Text: fmt.Sprintf("Logged in to %s!", oauthProv.Name()),
					}
				}

				// Anthropic: two-phase authorization code + PKCE flow.
				session, err := anthProv.StartAuthFlow()
				if err != nil {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Login failed: %v", err),
						IsError: true,
					}
				}

				return authOAuthMsg{
					providerName: oauthProv.Name(),
					url:          session.AuthorizeURL,
					waitCmd: func() tea.Msg {
						cred, err := anthProv.ExchangeAuthCode(session, 0)
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
						return CommandResultMsg{
							Text: fmt.Sprintf("Successfully logged in to %s!", oauthProv.Name()),
						}
					},
				}
			}
		},
	}
}

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

				providers := []string{"anthropic", "openrouter", "openai", "google"}
				for _, p := range providers {
					key, _ := resolver.Resolve(p)
					cred := store.Get(p)

					if key != "" {
						sb.WriteString(fmt.Sprintf("\n  %s: authenticated", p))
						if cred != nil {
							switch cred.Type {
							case auth.CredentialOAuth:
								if cred.IsExpired() {
									sb.WriteString(" (OAuth, token expired)")
								} else if cred.ExpiresAt > 0 {
									expires := time.UnixMilli(cred.ExpiresAt)
									sb.WriteString(fmt.Sprintf(" (OAuth, expires %s)",
										expires.Format("Jan 2 15:04")))
								} else {
									sb.WriteString(" (OAuth)")
								}
							case auth.CredentialAPIKey:
								sb.WriteString(" (API key)")
							case auth.CredentialHeader:
								sb.WriteString(" (custom header)")
							}
						} else {
							sb.WriteString(" (env var)")
						}
					} else {
						sb.WriteString(fmt.Sprintf("\n  %s: not configured", p))
					}
				}
				sb.WriteString("\n")

				return CommandResultMsg{Text: sb.String()}
			}
		},
	}
}
