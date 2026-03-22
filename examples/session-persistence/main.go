// Example: session-persistence demonstrates saving and resuming conversations
// across multiple program runs using session IDs.
//
// This example shows how to:
// - Create a new session and continue a multi-turn conversation
// - Save session state automatically
// - Resume a previous session by ID
// - List sessions and find recent ones
// - Work with session branches for exploring alternatives
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-...
//
//	# First run - creates a new session
//	go run ./examples/session-persistence
//
//	# The program will display a session ID, like: abc123def456
//	# Save that ID and run:
//	go run ./examples/session-persistence abc123def456
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/sdk"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY environment variable is required")
	}

	// Get session ID from command line if provided
	flag.Parse()
	sessionID := flag.Arg(0)

	// Determine session directory (~/.gi/sessions)
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	sessionDir := filepath.Join(home, ".gi", "sessions")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	// Create a new session
	fmt.Println("Creating new session...")
	s, err := sdk.NewSession(ctx,
		sdk.WithAPIKey("anthropic", apiKey),
		sdk.WithSessionDir(sessionDir),
		sdk.WithSystemPrompt("You are a helpful assistant for learning Go programming."),
	)
	if err != nil {
		log.Fatal(err)
	}

	// If a session ID was provided, resume that session instead
	if sessionID != "" {
		fmt.Printf("Resuming session %s...\n\n", sessionID)
		if err := s.Resume(sessionID); err != nil {
			log.Printf("Warning: could not resume session: %v\n", err)
		}
	} else {
		fmt.Printf("Session ID: %s\n", s.SessionID())
		fmt.Println("Save this ID to resume the conversation later.")
	}

	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	// Multi-turn conversation example
	conversationStarters := []string{
		"What is a goroutine?",
		"How do goroutines differ from OS threads?",
		"Can you show me a simple goroutine example?",
	}

	for i, prompt := range conversationStarters {
		fmt.Printf("[Turn %d] You: %s\n", i+1, prompt)
		fmt.Print("Assistant: ")

		events := s.Events()
		errCh := make(chan error, 1)
		go func() {
			errCh <- s.Prompt(ctx, prompt)
		}()

		for event := range events {
			switch event.Type {
			case agent.EventAssistantText:
				fmt.Print(event.Delta)
			case agent.EventToolExecStart:
				fmt.Fprintf(os.Stderr, "\n[tool: %s]\n", event.ToolName)
			case agent.EventAgentError:
				fmt.Fprintf(os.Stderr, "\nError: %v\n", event.Error)
			}
		}
		fmt.Println()

		if err := <-errCh; err != nil {
			log.Printf("Error: %v\n", err)
		}
	}

	fmt.Printf("\nSession %s saved. Resume with:\n", s.SessionID())
	fmt.Printf("  go run ./examples/session-persistence %s\n", s.SessionID())
}
