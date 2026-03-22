package refinery_gate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckCI_AllPassed(t *testing.T) {
	// Mock GitHub API response with all workflows passing
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"workflow_runs": []map[string]interface{}{
				{
					"id":         1001,
					"name":       "Build",
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1001",
				},
				{
					"id":         1002,
					"name":       "Lint",
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1002",
				},
				{
					"id":         1003,
					"name":       "Tests",
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1003",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	checker := &GateChecker{
		client:    server.Client(),
		owner:     "eamonsjm",
		repo:      "go_pi",
		token:     "fake-token",
		branch:    "main",
		workflows: []string{"Build", "Lint", "Tests"},
		apiURL:    server.URL + "/repos/eamonsjm/go_pi/actions/runs?branch=main&per_page=50",
	}

	status, err := checker.CheckCI(context.Background())

	if err != nil {
		t.Fatalf("CheckCI failed: %v", err)
	}

	if !status.Passed {
		t.Errorf("Expected gate to pass, but got: %s", status.Reason)
	}

	if len(status.WorkflowStatuses) != 3 {
		t.Errorf("Expected 3 workflows, got %d", len(status.WorkflowStatuses))
	}

	for name, wf := range status.WorkflowStatuses {
		if wf.Conclusion != "success" {
			t.Errorf("Workflow %s should have conclusion 'success', got '%s'", name, wf.Conclusion)
		}
	}
}

func TestCheckCI_OneWorkflowFailing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"workflow_runs": []map[string]interface{}{
				{
					"id":         1001,
					"name":       "Build",
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1001",
				},
				{
					"id":         1002,
					"name":       "Lint",
					"status":     "completed",
					"conclusion": "failure",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1002",
				},
				{
					"id":         1003,
					"name":       "Tests",
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1003",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	checker := &GateChecker{
		client:    server.Client(),
		owner:     "eamonsjm",
		repo:      "go_pi",
		token:     "fake-token",
		branch:    "main",
		workflows: []string{"Build", "Lint", "Tests"},
		apiURL:    server.URL + "/repos/eamonsjm/go_pi/actions/runs?branch=main&per_page=50",
	}

	status, err := checker.CheckCI(context.Background())

	if err != nil {
		t.Fatalf("CheckCI failed: %v", err)
	}

	if status.Passed {
		t.Errorf("Expected gate to fail when Lint workflow fails")
	}

	if status.WorkflowStatuses["Lint"].Conclusion != "failure" {
		t.Errorf("Expected Lint to have failure conclusion")
	}
}

func TestCheckCI_WorkflowInProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"workflow_runs": []map[string]interface{}{
				{
					"id":         1001,
					"name":       "Build",
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1001",
				},
				{
					"id":         1002,
					"name":       "Lint",
					"status":     "in_progress",
					"conclusion": "",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1002",
				},
				{
					"id":         1003,
					"name":       "Tests",
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1003",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	checker := &GateChecker{
		client:    server.Client(),
		owner:     "eamonsjm",
		repo:      "go_pi",
		token:     "fake-token",
		branch:    "main",
		workflows: []string{"Build", "Lint", "Tests"},
		apiURL:    server.URL + "/repos/eamonsjm/go_pi/actions/runs?branch=main&per_page=50",
	}

	status, err := checker.CheckCI(context.Background())

	if err != nil {
		t.Fatalf("CheckCI failed: %v", err)
	}

	if status.Passed {
		t.Errorf("Expected gate to fail when Lint workflow is in progress")
	}

	if status.WorkflowStatuses["Lint"].Status != "in_progress" {
		t.Errorf("Expected Lint to have in_progress status")
	}
}

func TestCheckCI_MissingWorkflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"workflow_runs": []map[string]interface{}{
				{
					"id":         1001,
					"name":       "Build",
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1001",
				},
				{
					"id":         1003,
					"name":       "Tests",
					"status":     "completed",
					"conclusion": "success",
					"html_url":   "https://github.com/eamonsjm/go_pi/actions/runs/1003",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	checker := &GateChecker{
		client:    server.Client(),
		owner:     "eamonsjm",
		repo:      "go_pi",
		token:     "fake-token",
		branch:    "main",
		workflows: []string{"Build", "Lint", "Tests"},
		apiURL:    server.URL + "/repos/eamonsjm/go_pi/actions/runs?branch=main&per_page=50",
	}

	status, err := checker.CheckCI(context.Background())

	if err != nil {
		t.Fatalf("CheckCI failed: %v", err)
	}

	if status.Passed {
		t.Errorf("Expected gate to fail when a required workflow is missing")
	}

	if status.WorkflowStatuses["Lint"].Conclusion != "failure" {
		t.Errorf("Expected missing Lint workflow to be marked as failure")
	}
}

// captureTransport records the request URL and returns a canned JSON response.
type captureTransport struct {
	capturedURL string
}

func (ct *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ct.capturedURL = req.URL.String()
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"workflow_runs":[]}`)),
	}, nil
}

func TestFetchWorkflowRuns_BranchNameURLEncoded(t *testing.T) {
	transport := &captureTransport{}
	checker := &GateChecker{
		client: &http.Client{Transport: transport},
		owner:  "owner",
		repo:   "repo",
		token:  "token",
		branch: "feature/add-thing#123",
		// apiURL intentionally empty — exercises the URL construction code path
	}

	_, err := checker.fetchWorkflowRuns(context.Background())
	if err != nil {
		t.Fatalf("fetchWorkflowRuns failed: %v", err)
	}

	wantEncoded := "branch=feature%2Fadd-thing%23123"
	if !strings.Contains(transport.capturedURL, wantEncoded) {
		t.Errorf("Expected URL to contain %q, got: %s", wantEncoded, transport.capturedURL)
	}
}
