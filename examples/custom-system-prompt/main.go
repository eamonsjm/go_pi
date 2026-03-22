// Example: custom-system-prompt demonstrates setting a custom system prompt
// to guide the AI's behavior for a specific task.
//
// This example shows how to:
// - Set a custom system prompt for specialized behavior
// - Modify the prompt at runtime
// - Use the prompt to constrain AI outputs
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-...
//	go run ./examples/custom-system-prompt
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/sdk"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY environment variable is required")
	}

	// Create a session with a custom system prompt for a code reviewer
	codeReviewPrompt := `You are an expert code reviewer. Your role is to:
- Identify bugs, security issues, and performance problems
- Suggest improvements for readability and maintainability
- Explain the reasoning behind your suggestions
- Be constructive and helpful in your feedback
- Focus on the most important issues first`

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := sdk.NewSession(ctx,
		sdk.WithAPIKey("anthropic", apiKey),
		sdk.WithSystemPrompt(codeReviewPrompt),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	// Handle Ctrl+C. The goroutine exits via ctx.Done when the prompt
	// ends, and signal.Stop unregisters the channel.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()

	// Example code snippet to review
	codeSnippet := `
func calculateTotal(items []Item) float64 {
	var total float64
	for _, item := range items {
		if item.Price > 0 {
			total = total + item.Price
		}
	}
	return total
}
`

	prompt := fmt.Sprintf("Please review this Go code:\n%s", codeSnippet)

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

	// You can also modify the system prompt at runtime
	newPrompt := `You are now a Python expert. Rewrite the above code in Python.`
	s.SetSystemPrompt(newPrompt)

	fmt.Println()
	fmt.Println("--- After changing system prompt ---")
	fmt.Println()

	events = s.Events()
	errCh = make(chan error, 1)
	go func() {
		errCh <- s.Prompt(ctx, "Can you rewrite that code in Python with the same logic?")
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
