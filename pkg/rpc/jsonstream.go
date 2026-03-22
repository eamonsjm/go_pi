package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

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
			var sb strings.Builder
			for scanner.Scan() {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(scanner.Text())
			}
			if err := scanner.Err(); err != nil {
				data, mErr := json.Marshal(Event{Type: "error", Error: fmt.Sprintf("reading stdin: %v", err)})
				if mErr != nil {
					log.Fatalf("jsonstream: failed to marshal error event: %v", mErr)
				}
				if _, wErr := fmt.Fprintf(os.Stdout, "%s\n", data); wErr != nil {
					log.Printf("jsonstream: failed to write error: %v", wErr)
				}
				os.Exit(1)
			}
			prompt = sb.String()
		}
		if prompt == "" {
			data, mErr := json.Marshal(Event{Type: "error", Error: "no prompt provided"})
			if mErr != nil {
				log.Fatalf("jsonstream: failed to marshal error event: %v", mErr)
			}
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
		defer func() {
			if r := recover(); r != nil {
				promptErr = fmt.Errorf("panic in Prompt: %v", r)
			}
			close(done)
		}()
		promptErr = agentLoop.Prompt(ctx, prompt)
	}()

	var sawError bool
loop:
	for {
		select {
		case event, ok := <-events:
			if !ok {
				break loop
			}
			ev := EventFromAgent(event)
			if err := enc.Encode(ev); err != nil {
				log.Printf("jsonstream: write failed: %v", err)
				cancel()
				break loop
			}

			if event.Type == agent.EventAgentError {
				sawError = true
				break loop
			}
			if event.Type == agent.EventAgentEnd {
				break loop
			}
		case <-done:
			// Prompt goroutine finished (possibly via panic recovery)
			// before closing the events channel.
			break loop
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
