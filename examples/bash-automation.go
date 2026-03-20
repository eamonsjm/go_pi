// Example: bash-automation demonstrates using gi as an AI-powered automation tool
// that can read files, run bash commands, and make decisions based on output.
//
// This example shows how to:
// - Ask the AI to analyze and modify files
// - Give the AI access to run bash commands
// - Create automated workflows (e.g., code review, test execution)
// - Handle tool execution and results
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-...
//	go run ./examples/bash-automation
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/sdk"
	"github.com/ejm/go_pi/pkg/tools"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY environment variable is required")
	}

	// Create a tool registry with the tools the AI can use
	registry := tools.NewRegistry()
	registry.Register(&tools.ReadTool{})
	registry.Register(&tools.BashTool{})
	registry.Register(&tools.GlobTool{})
	registry.Register(&tools.GrepTool{})

	s, err := sdk.NewSession(
		sdk.WithAPIKey("anthropic", apiKey),
		sdk.WithTools(registry),
		sdk.WithWorkingDir("."),
		sdk.WithSystemPrompt(`You are an intelligent automation assistant that can:
- Read and analyze files
- Run bash commands to inspect or modify the system
- Make decisions based on command output
- Provide clear explanations of what you're doing

When asked to perform tasks, use the available tools to:
1. Analyze the current state of the system
2. Execute necessary commands
3. Verify the results
4. Report back with a summary`),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	// Example automation tasks
	tasks := []string{
		"How many lines of Go code are in the cmd/gi directory? Use bash and glob tools to find all .go files and count the total lines.",
		"Show me the structure of the project by listing the main directories and their purposes",
		"Check if there's a go.mod file and tell me what are the top-level modules/dependencies",
	}

	for i, task := range tasks {
		fmt.Printf("\n=== Task %d ===\n%s\n\n", i+1, task)
		fmt.Println("AI Response:")
		fmt.Println("---")

		events := s.Events()
		errCh := make(chan error, 1)
		go func() {
			errCh <- s.Prompt(ctx, task)
		}()

		for event := range events {
			switch event.Type {
			case agent.EventAssistantText:
				fmt.Print(event.Delta)
			case agent.EventToolExecStart:
				fmt.Fprintf(os.Stderr, "\n[executing tool: %s]\n", event.ToolName)
			case agent.EventToolResult:
				fmt.Fprintf(os.Stderr, "[tool output received]\n")
			case agent.EventAgentError:
				fmt.Fprintf(os.Stderr, "\nError: %v\n", event.Error)
			}
		}
		fmt.Println("\n---")

		if err := <-errCh; err != nil {
			log.Printf("Error: %v\n", err)
		}
	}
}
