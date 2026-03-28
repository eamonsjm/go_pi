package main

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed prompts
var promptsFS embed.FS

// defaultPrompt is the base system prompt for non-OAuth paths.
// Loaded from prompts/default.md at init time.
var defaultPrompt string

// oauthPrompts maps provider name → base prompt for OAuth paths.
// Populated at init time by scanning prompts/oauth_<provider>.md files.
var oauthPrompts map[string]string

func init() {
	b, err := fs.ReadFile(promptsFS, "prompts/default.md")
	if err != nil {
		panic(fmt.Sprintf("prompts: read default.md: %v", err))
	}
	defaultPrompt = strings.TrimSpace(string(b))

	oauthPrompts = make(map[string]string)

	entries, err := fs.ReadDir(promptsFS, "prompts")
	if err != nil {
		panic(fmt.Sprintf("prompts: read dir: %v", err))
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "oauth_") || !strings.HasSuffix(name, ".md") {
			continue
		}
		provider := strings.TrimSuffix(strings.TrimPrefix(name, "oauth_"), ".md")
		b, err := fs.ReadFile(promptsFS, "prompts/"+name)
		if err != nil {
			panic(fmt.Sprintf("prompts: read %s: %v", name, err))
		}
		oauthPrompts[provider] = strings.TrimSpace(string(b))
	}
}
