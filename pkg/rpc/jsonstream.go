package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/ejm/go_pi/pkg/agent"
)

// RunJSONStream runs the agent in JSON event stream mode. It reads a prompt
// from args (or stdin if args is empty), runs the agent, and writes each event
// as a newline-delimited JSON object to stdout.
func RunJSONStream(agentLoop *agent.AgentLoop, prompt string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS interrupt. The goroutine exits via ctx.Done when the
	// stream ends, and signal.Stop unregisters the channel.
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

	if prompt == "" {
		info, err := os.Stdin.Stat()
		if err == nil && info.Mode()&os.ModeCharDevice == 0 {
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				if prompt != "" {
					prompt += "\n"
				}
				prompt += scanner.Text()
			}
		}
		if prompt == "" {
			data, _ := json.Marshal(Event{Type: "error", Error: "no prompt provided"})
			if _, err := fmt.Fprintf(os.Stdout, "%s\n", data); err != nil {
				log.Printf("jsonstream: failed to write error: %v", err)
			}
			os.Exit(1)
		}
	}

	enc := json.NewEncoder(os.Stdout)
	events := agentLoop.Events()

	done := make(chan struct{})
	var promptErr error
	go func() {
		defer close(done)
		promptErr = agentLoop.Prompt(ctx, prompt)
	}()

	var sawError bool
	for event := range events {
		ev := EventFromAgent(event)
		if err := enc.Encode(ev); err != nil {
			log.Printf("jsonstream: write failed: %v", err)
			cancel()
			break
		}

		if event.Type == agent.EventAgentError {
			sawError = true
			break
		}
		if event.Type == agent.EventAgentEnd {
			break
		}
	}

	// Wait for the Prompt goroutine to finish so it doesn't outlive this
	// function. Context cancellation (via defer cancel above) ensures
	// Prompt returns promptly even if the event loop exited early.
	<-done

	// If Prompt returned an error but no error event was emitted to the
	// stream, write a synthetic error event so callers see the failure.
	if promptErr != nil && !sawError {
		ev := Event{Type: "error", Error: promptErr.Error()}
		if err := enc.Encode(ev); err != nil {
			log.Printf("jsonstream: failed to write error: %v", err)
		}
	}
}
