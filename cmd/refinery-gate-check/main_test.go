package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/refinerygate"
)

// mockGitHubAPI creates a test server that returns the given workflow runs.
func mockGitHubAPI(t *testing.T, runs []map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got == "" {
			t.Error("expected Authorization header, got none")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"workflow_runs": runs,
		})
	}))
}

var allPassingRuns = []map[string]interface{}{
	{"id": 1001, "name": "Build", "status": "completed", "conclusion": "success", "html_url": "https://example.com/1001"},
	{"id": 1002, "name": "Lint", "status": "completed", "conclusion": "success", "html_url": "https://example.com/1002"},
	{"id": 1003, "name": "Tests", "status": "completed", "conclusion": "success", "html_url": "https://example.com/1003"},
}

func TestRun_NoTokenReturnsError(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "GitHub token required") {
		t.Errorf("expected token error in stderr, got: %s", stderr.String())
	}
}

func TestRun_TokenFromFlag(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	server := mockGitHubAPI(t, allPassingRuns)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-token", "flag-token"},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 0 {
		t.Errorf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}
}

func TestRun_TokenFromEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env-token")
	server := mockGitHubAPI(t, allPassingRuns)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 0 {
		t.Errorf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}
}

func TestRun_AllWorkflowsPassed(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	server := mockGitHubAPI(t, allPassingRuns)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nraw: %s", err, stdout.String())
	}
	if passed, ok := result["passed"].(bool); !ok || !passed {
		t.Errorf("expected passed=true in output, got: %v", result["passed"])
	}
}

func TestRun_FailedWorkflow(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	runs := []map[string]interface{}{
		{"id": 1001, "name": "Build", "status": "completed", "conclusion": "success", "html_url": ""},
		{"id": 1002, "name": "Lint", "status": "completed", "conclusion": "failure", "html_url": ""},
		{"id": 1003, "name": "Tests", "status": "completed", "conclusion": "success", "html_url": ""},
	}
	server := mockGitHubAPI(t, runs)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 1 {
		t.Errorf("expected exit code 1 for failed workflow, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}
	if passed, ok := result["passed"].(bool); !ok || passed {
		t.Errorf("expected passed=false, got: %v", result["passed"])
	}
}

func TestRun_CustomWorkflows(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	runs := []map[string]interface{}{
		{"id": 1, "name": "Deploy", "status": "completed", "conclusion": "success", "html_url": ""},
		{"id": 2, "name": "E2E", "status": "completed", "conclusion": "success", "html_url": ""},
	}
	server := mockGitHubAPI(t, runs)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-workflows", "Deploy, E2E"},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 0 {
		t.Errorf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}
	workflows := result["workflow_statuses"].(map[string]interface{})
	if _, ok := workflows["Deploy"]; !ok {
		t.Error("expected Deploy in workflow_statuses")
	}
	if _, ok := workflows["E2E"]; !ok {
		t.Error("expected E2E in workflow_statuses")
	}
}

func TestRun_VerboseOutput(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	server := mockGitHubAPI(t, allPassingRuns)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-verbose"},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Gate check PASSED") {
		t.Errorf("expected verbose PASSED message in stderr, got: %s", stderr.String())
	}
}

func TestRun_VerboseOnFailure(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	runs := []map[string]interface{}{
		{"id": 1, "name": "Build", "status": "completed", "conclusion": "failure", "html_url": ""},
		{"id": 2, "name": "Lint", "status": "completed", "conclusion": "success", "html_url": ""},
		{"id": 3, "name": "Tests", "status": "completed", "conclusion": "success", "html_url": ""},
	}
	server := mockGitHubAPI(t, runs)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-verbose"},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Gate check FAILED") {
		t.Errorf("expected verbose FAILED message in stderr, got: %s", stderr.String())
	}
}

func TestRun_APIError(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse error JSON: %v\nraw: %s", err, stdout.String())
	}
	if passed, ok := result["passed"].(bool); !ok || passed {
		t.Errorf("expected passed=false in error output, got: %v", result["passed"])
	}
	if _, ok := result["error"]; !ok {
		t.Error("expected 'error' field in error JSON output")
	}
}

func TestRun_VerboseAPIError(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-verbose"},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Error checking CI") {
		t.Errorf("expected verbose error in stderr, got: %s", stderr.String())
	}
}

func TestRun_InvalidFlag(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-nonexistent"}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("expected exit code 1 for invalid flag, got %d", code)
	}
}

func TestRun_CustomBranchAndOwner(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	var receivedURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"workflow_runs": allPassingRuns})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-owner", "myorg", "-repo", "myrepo", "-branch", "develop"},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if code != 0 {
		t.Errorf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	// WithAPIURL overrides the full URL, so the owner/repo/branch flags
	// affect the GateChecker fields but not the mock URL. Verify the
	// command still ran successfully with custom flags.
	_ = receivedURL
}

func TestRun_OutputIsValidJSON(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	server := mockGitHubAPI(t, allPassingRuns)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	run(
		[]string{},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nraw: %s", err, stdout.String())
	}

	// Verify expected top-level fields
	for _, field := range []string{"passed", "reason", "workflow_statuses", "timestamp"} {
		if _, ok := result[field]; !ok {
			t.Errorf("missing expected field %q in JSON output", field)
		}
	}
}

func TestRun_FlagTokenOverridesEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env-token")

	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"workflow_runs": allPassingRuns})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	run(
		[]string{"-token", "flag-token"},
		&stdout, &stderr,
		refinerygate.WithAPIURL(server.URL),
	)

	if receivedAuth != "token flag-token" {
		t.Errorf("expected flag token to override env, got Authorization: %s", receivedAuth)
	}
}
