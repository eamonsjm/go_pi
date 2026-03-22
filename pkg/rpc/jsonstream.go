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
	"syscall"

	"github.com/ejm/go_pi/pkg/agent"
	"golang.org/x/sync/errgroup"
)

// RunJSONStream runs the agent in JSON event stream mode. It reads a prompt
// from args (or stdin if args is empty), runs the agent, and writes each event
// as a newline-delimited JSON object to stdout. It returns an exit code.
func RunJSONStream(agentLoop *agent.AgentLoop, prompt string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS interrupt. The goroutine exits via ctx.Done when the
	// stream ends, and signal.Stop unregisters the channel.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
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
					log.Printf("jsonstream: failed to marshal error event: %v", mErr)
					return 1
				}
				if _, wErr := fmt.Fprintf(os.Stdout, "%s\n", data); wErr != nil {
					log.Printf("jsonstream: failed to write error: %v", wErr)
				}
				return 1
			}
			prompt = sb.String()
		}
		if prompt == "" {
			data, mErr := json.Marshal(Event{Type: "error", Error: "no prompt provided"})
			if mErr != nil {
				log.Printf("jsonstream: failed to marshal error event: %v", mErr)
				return 1
			}
			if _, err := fmt.Fprintf(os.Stdout, "%s\n", data); err != nil {
				log.Printf("jsonstream: failed to write error: %v", err)
			}
			return 1
		}
	}

	enc := json.NewEncoder(os.Stdout)
	events := agentLoop.Events()

	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic in Prompt: %v", r)
			}
		}()
		return agentLoop.Prompt(gCtx, prompt)
	})

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
		case <-gCtx.Done():
			// Prompt goroutine returned an error (possibly via panic
			// recovery) before the events channel closed.
			break loop
		}
	}

	// Wait for the Prompt goroutine to finish so it doesn't outlive this
	// function. Context cancellation (via defer cancel above) ensures
	// Prompt returns promptly even if the event loop exited early.
	promptErr := g.Wait()

	// If Prompt returned an error but no error event was emitted to the
	// stream, write a synthetic error event so callers see the failure.
	if promptErr != nil && !sawError {
		ev := Event{Type: "error", Error: promptErr.Error()}
		if err := enc.Encode(ev); err != nil {
			log.Printf("jsonstream: failed to write error: %v", err)
		}
	}

	if sawError || promptErr != nil {
		return 1
	}
	return 0
}
