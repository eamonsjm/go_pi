package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ejm/go_pi/pkg/auth"
)

// NewEncryptCommand creates the /encrypt slash command that enables SOPS
// encryption on the auth store. It generates an age key if one doesn't
// exist, encrypts auth.json in place, and saves a sops-config.json marker.
func NewEncryptCommand(store *auth.Store) *SlashCommand {
	return &SlashCommand{
		Name:        "encrypt",
		Description: "Encrypt auth.json with SOPS (generates age key if needed)",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				configDir := store.ConfigDir()
				keyPath := store.AgeKeyPath()

				// Already encrypted and key matches — nothing to do.
				if store.SopsKey() != "" && store.Encrypted() {
					return CommandResultMsg{
						Text: fmt.Sprintf("Auth store is already encrypted.\n  Key: %s\n  Public key: %s",
							keyPath, store.SopsKey()),
					}
				}

				// Ensure age key exists; generate if missing.
				var pubKey string
				if _, err := os.Stat(keyPath); os.IsNotExist(err) {
					id, err := auth.GenerateAgeKey(keyPath)
					if err != nil {
						return CommandResultMsg{
							Text:    fmt.Sprintf("Failed to generate age key: %v", err),
							IsError: true,
						}
					}
					pubKey = id.Recipient().String()
				} else {
					id, err := auth.LoadAgeKey(keyPath)
					if err != nil {
						return CommandResultMsg{
							Text:    fmt.Sprintf("Failed to load age key: %v", err),
							IsError: true,
						}
					}
					pubKey = id.Recipient().String()
				}

				// Enable encryption on the store and re-save.
				store.SetSopsKey(pubKey)
				if err := store.Save(); err != nil {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Failed to encrypt auth store: %v", err),
						IsError: true,
					}
				}

				// Persist the sops-config marker.
				sopsConfig := &auth.SopsConfig{
					Enabled:    true,
					AgeKeyPath: keyPath,
				}
				if err := auth.SaveSopsConfig(configDir, sopsConfig); err != nil {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Encrypted auth.json but failed to save sops-config: %v", err),
						IsError: true,
					}
				}

				return CommandResultMsg{
					Text: fmt.Sprintf("Auth store encrypted with SOPS.\n"+
						"  Key file: %s\n"+
						"  Public key: %s\n\n"+
						"⚠ Back up your age key! Without it, encrypted credentials cannot be recovered.\n"+
						"  Copy it to a secure location:\n"+
						"    cp %s /path/to/secure/backup/\n"+
						"  Or add it to your password manager.\n"+
						"  To decrypt before a key change: /decrypt",
						keyPath, pubKey, keyPath),
				}
			}
		},
	}
}

// NewDecryptCommand creates the /decrypt slash command that disables SOPS
// encryption and writes auth.json back as plaintext.
//
// Without --force: shows a warning and aborts.
// With --force: proceeds with decryption.
func NewDecryptCommand(store *auth.Store) *SlashCommand {
	return &SlashCommand{
		Name:        "decrypt",
		Description: "Decrypt auth.json back to plaintext (disables SOPS encryption)",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				if store.SopsKey() == "" && !store.Encrypted() {
					return CommandResultMsg{
						Text: "Auth store is not encrypted — nothing to do.",
					}
				}

				force := strings.TrimSpace(args) == "--force"
				if !force {
					return CommandResultMsg{
						Text: "⚠ This will remove SOPS encryption and store credentials as plaintext.\n" +
							"Your age key will remain at " + store.AgeKeyPath() + " for re-encryption.\n\n" +
							"Run /decrypt --force to confirm.",
					}
				}

				// Disable encryption and re-save as plaintext.
				store.SetSopsKey("")
				if err := store.Save(); err != nil {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Failed to save decrypted auth store: %v", err),
						IsError: true,
					}
				}

				// Remove the sops-config marker.
				configDir := store.ConfigDir()
				if err := auth.RemoveSopsConfig(configDir); err != nil {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Decrypted auth.json but failed to remove sops-config: %v", err),
						IsError: true,
					}
				}

				return CommandResultMsg{
					Text: "Auth store decrypted. Credentials are now stored as plaintext.\n" +
						"Your age key is still at " + store.AgeKeyPath() + " — run /encrypt to re-enable encryption.",
				}
			}
		},
	}
}

// NewSopsStatusCommand creates the /sops-status slash command that shows
// the current encryption status of the auth store.
func NewSopsStatusCommand(store *auth.Store) *SlashCommand {
	return &SlashCommand{
		Name:        "sops-status",
		Description: "Show SOPS encryption status for auth store",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				var sb strings.Builder
				sb.WriteString("SOPS Encryption Status:\n")

				configDir := store.ConfigDir()
				keyPath := store.AgeKeyPath()

				// Config status.
				cfg, cfgErr := auth.LoadSopsConfig(configDir)
				if cfgErr != nil {
					fmt.Fprintf(&sb, "\n  Config: error (%v)", cfgErr)
				} else if cfg.Enabled {
					sb.WriteString("\n  Config: enabled")
					if cfg.AgeKeyPath != "" {
						fmt.Fprintf(&sb, " (key: %s)", cfg.AgeKeyPath)
					}
				} else {
					sb.WriteString("\n  Config: not enabled")
				}

				// File encryption status.
				if store.Encrypted() {
					sb.WriteString("\n  Auth file: encrypted")
				} else {
					sb.WriteString("\n  Auth file: plaintext")
				}

				// Runtime SOPS key.
				if pub := store.SopsKey(); pub != "" {
					fmt.Fprintf(&sb, "\n  Active key: %s", pub)
				} else {
					sb.WriteString("\n  Active key: none (saves will be plaintext)")
				}

				// Age key file status.
				if info, err := os.Stat(keyPath); err == nil {
					modified := time.Since(info.ModTime()).Truncate(time.Second)
					fmt.Fprintf(&sb, "\n  Key file: %s (modified: %s ago)", keyPath, modified)
				} else if os.IsNotExist(err) {
					fmt.Fprintf(&sb, "\n  Key file: %s (not found)", keyPath)
				} else {
					fmt.Fprintf(&sb, "\n  Key file: %s (error: %v)", keyPath, err)
				}

				sb.WriteString("\n")
				return CommandResultMsg{Text: sb.String()}
			}
		},
	}
}

// NewExportAgeKeyCommand creates the /export-age-key slash command that
// prints the age public key for sharing with team KMS policies.
func NewExportAgeKeyCommand(store *auth.Store) *SlashCommand {
	return &SlashCommand{
		Name:        "export-age-key",
		Description: "Print age public key (for sharing with team KMS policies)",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				keyPath := store.AgeKeyPath()

				id, err := auth.LoadAgeKey(keyPath)
				if err != nil {
					return CommandResultMsg{
						Text:    fmt.Sprintf("No age key found at %s.\nRun /encrypt to generate one.", keyPath),
						IsError: true,
					}
				}

				pub := id.Recipient().String()
				return CommandResultMsg{
					Text: fmt.Sprintf("Age public key:\n  %s\n\nKey file: %s", pub, keyPath),
				}
			}
		},
	}
}
