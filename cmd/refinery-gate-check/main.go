package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ejm/go_pi/pkg/refinery_gate"
)

func main() {
	owner := flag.String("owner", "eamonsjm", "GitHub repository owner")
	repo := flag.String("repo", "go_pi", "GitHub repository name")
	token := flag.String("token", "", "GitHub API token (or GITHUB_TOKEN env var)")
	branch := flag.String("branch", "main", "Git branch to check")
	workflowsStr := flag.String("workflows", "Build,Lint,Tests", "Comma-separated workflow names to check")
	timeoutSec := flag.Int("timeout", 30, "API timeout in seconds")
	verbose := flag.Bool("verbose", false, "Verbose output")
	flag.Parse()

	// Get token from environment if not provided
	if *token == "" {
		*token = os.Getenv("GITHUB_TOKEN")
		if *token == "" {
			fmt.Fprintf(os.Stderr, "ERROR: GitHub token required (--token or GITHUB_TOKEN env var)\n")
			os.Exit(1)
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
	checker := refinery_gate.NewGateChecker(*owner, *repo, *token, *branch, workflows)
	status, err := checker.CheckCI(ctx)

	if err != nil {
		output := map[string]interface{}{
			"passed": false,
			"reason": fmt.Sprintf("Gate check failed: %v", err),
			"error":  err.Error(),
		}
		if *verbose {
			fmt.Fprintf(os.Stderr, "Error checking CI: %v\n", err)
		}
		jsonOut, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(jsonOut))
		os.Exit(1)
	}

	// Output gate status as JSON
	jsonOut, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Failed to marshal output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(jsonOut))

	// Exit with appropriate status
	if !status.Passed {
		if *verbose {
			fmt.Fprintf(os.Stderr, "Gate check FAILED: %s\n", status.Reason)
		}
		os.Exit(1)
	}

	if *verbose {
		fmt.Fprintf(os.Stderr, "Gate check PASSED: %s\n", status.Reason)
	}
}
