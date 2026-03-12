// Example: simple-chat demonstrates basic SDK usage with streaming output.
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-...
//	go run ./examples/simple-chat "Explain what Go interfaces are"
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/sdk"
)

func main() {
	prompt := strings.Join(os.Args[1:], " ")
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "Usage: simple-chat <prompt>")
		os.Exit(1)
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY environment variable is required")
	}

	s, err := sdk.NewSession(
		sdk.WithAPIKey("anthropic", apiKey),
		sdk.WithSystemPrompt("You are a helpful coding assistant. Be concise."),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	// Start reading events before prompting.
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
		log.Fatal(err)
	}
}
