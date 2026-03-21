// Example: code-review demonstrates using the SDK to build a code review tool.
//
// It reads a file, sends it to the agent for review, and prints the feedback.
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-...
//	go run ./examples/code-review path/to/file.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/sdk"
	"github.com/ejm/go_pi/pkg/tools"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: code-review <file>")
		os.Exit(1)
	}
	filePath := os.Args[1]

	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Cannot read %s: %v", filePath, err)
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY environment variable is required")
	}

	// Use a read-only tool registry — no write/bash/edit tools for review.
	registry := tools.NewRegistry()
	registry.Register(&tools.ReadTool{})
	registry.Register(&tools.GlobTool{})
	registry.Register(&tools.GrepTool{})

	s, err := sdk.NewSession(
		sdk.WithAPIKey("anthropic", apiKey),
		sdk.WithModel("claude-sonnet-4-20250514"),
		sdk.WithTools(registry),
		sdk.WithSystemPrompt("You are a code reviewer. Review code for bugs, security issues, style problems, and suggest improvements. Be specific and actionable."),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	events := s.Events()

	prompt := fmt.Sprintf("Review this file (%s):\n\n```\n%s\n```", filePath, strings.TrimSpace(string(data)))

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Prompt(ctx, prompt)
	}()

	var textOutput strings.Builder
	for event := range events {
		switch event.Type {
		case agent.EventAssistantText:
			fmt.Print(event.Delta)
			textOutput.WriteString(event.Delta)
		case agent.EventUsageUpdate:
			if event.Usage != nil {
				fmt.Fprintf(os.Stderr, "\n[tokens: %d in, %d out]\n",
					event.Usage.InputTokens, event.Usage.OutputTokens)
			}
		}
	}
	fmt.Println()

	if err := <-errCh; err != nil {
		log.Fatal(err)
	}

	// Show final message count.
	msgs := s.Messages()
	userCount, assistantCount := 0, 0
	for _, m := range msgs {
		switch m.Role {
		case ai.RoleUser:
			userCount++
		case ai.RoleAssistant:
			assistantCount++
		}
	}
	fmt.Fprintf(os.Stderr, "[session: %s, messages: %d user / %d assistant]\n",
		s.SessionID(), userCount, assistantCount)
}
