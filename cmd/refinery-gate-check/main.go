package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ejm/go_pi/pkg/refinery_gate"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer, opts ...refinery_gate.GateOption) int {
	fs := flag.NewFlagSet("refinery-gate-check", flag.ContinueOnError)
	fs.SetOutput(stderr)

	owner := fs.String("owner", "eamonsjm", "GitHub repository owner")
	repo := fs.String("repo", "go_pi", "GitHub repository name")
	token := fs.String("token", "", "GitHub API token (or GITHUB_TOKEN env var)")
	branch := fs.String("branch", "main", "Git branch to check")
	workflowsStr := fs.String("workflows", "Build,Lint,Tests", "Comma-separated workflow names to check")
	timeoutSec := fs.Int("timeout", 30, "API timeout in seconds")
	verbose := fs.Bool("verbose", false, "Verbose output")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	// Get token from environment if not provided
	if *token == "" {
		*token = os.Getenv("GITHUB_TOKEN")
		if *token == "" {
			_, _ = fmt.Fprintf(stderr, "ERROR: GitHub token required (--token or GITHUB_TOKEN env var)\n")
			return 1
		}
	}

	// Parse workflows
	workflows := []string{}
	if *workflowsStr != "" {
		for _, w := range strings.Split(*workflowsStr, ",") {
			workflows = append(workflows, strings.TrimSpace(w))
		}
	}

	// Create context with timeout and signal cancellation
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// Create gate checker and run CI check
	checker := refinery_gate.NewGateChecker(*owner, *repo, *token, *branch, workflows, opts...)
	status, err := checker.CheckCI(ctx)

	if err != nil {
		output := map[string]interface{}{
			"passed": false,
			"reason": fmt.Sprintf("Gate check failed: %v", err),
			"error":  err.Error(),
		}
		if *verbose {
			_, _ = fmt.Fprintf(stderr, "Error checking CI: %v\n", err)
		}
		jsonOut, _ := json.MarshalIndent(output, "", "  ")
		_, _ = fmt.Fprintln(stdout, string(jsonOut))
		return 1
	}

	// Output gate status as JSON
	jsonOut, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ERROR: Failed to marshal output: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintln(stdout, string(jsonOut))

	// Exit with appropriate status
	if !status.Passed {
		if *verbose {
			_, _ = fmt.Fprintf(stderr, "Gate check FAILED: %s\n", status.Reason)
		}
		return 1
	}

	if *verbose {
		_, _ = fmt.Fprintf(stderr, "Gate check PASSED: %s\n", status.Reason)
	}
	return 0
}
