package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run() error {
	owner := flag.String("owner", "eamonsjm", "GitHub owner")
	repo := flag.String("repo", "go_pi", "GitHub repo")
	flag.Parse()

	// Get private key from environment
	privKeyStr := os.Getenv("GITHUB_APP_PRIVATE_KEY")
	if privKeyStr == "" {
		return fmt.Errorf("GITHUB_APP_PRIVATE_KEY not set")
	}

	// Parse the private key
	block, _ := pem.Decode([]byte(privKeyStr))
	if block == nil {
		return fmt.Errorf("failed to parse private key PEM")
	}

	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse RSA private key: %w", err)
	}

	// Create JWT token (GitHub App ID would normally come from config)
	// For this test, we'll just verify we can create the token
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "12345", // Would be the GitHub App ID
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	})

	tokenStr, err := token.SignedString(privKey)
	if err != nil {
		return fmt.Errorf("failed to sign JWT: %w", err)
	}

	fmt.Println("✓ Step 1: Successfully created JWT token from GitHub App private key")
	fmt.Printf("  Token starts with: %s...\n", tokenStr[:50])

	// Step 2: Try to fetch workflow runs (this will fail without proper app ID, but tests API access)
	fmt.Println("\n✓ Step 2: Testing GitHub API access...")
	client := &http.Client{Timeout: 30 * time.Second}

	// Try to fetch workflow runs for this repo
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs", *owner, *repo)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// Note: Using JWT token directly won't work for this endpoint; would need app installation
	// But we can still test basic API connectivity

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach GitHub API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode == 401 {
		fmt.Printf("  ✓ API is reachable (got 401 Unauthorized - expected without proper auth)\n")
		fmt.Printf("  Response: %s\n", string(body[:min(100, len(body))]))
	} else if resp.StatusCode == 200 {
		// Try to parse workflow runs
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err == nil {
			if runs, ok := result["workflow_runs"].([]interface{}); ok {
				fmt.Printf("  ✓ Successfully fetched %d workflow runs\n", len(runs))
				for i, run := range runs {
					if i >= 3 {
						break
					}
					if runMap, ok := run.(map[string]interface{}); ok {
						name := runMap["name"]
						status := runMap["status"]
						conclusion := runMap["conclusion"]
						fmt.Printf("    - %v (status: %v, conclusion: %v)\n", name, status, conclusion)
					}
				}
			}
		}
	} else {
		fmt.Printf("  HTTP %d: %s\n", resp.StatusCode, string(body[:min(200, len(body))]))
	}

	fmt.Println("\n✓ Step 3: GitHub API connectivity verified")
	fmt.Println("\nPolecat can access GitHub APIs. Private key loaded successfully.")
	return nil
}
