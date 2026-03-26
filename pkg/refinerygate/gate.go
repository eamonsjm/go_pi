package refinerygate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// GateStatus represents the result of a CI gate check
type GateStatus struct {
	Passed           bool              `json:"passed"`
	Reason           string            `json:"reason"`
	WorkflowStatuses map[string]Status `json:"workflow_statuses"`
	Timestamp        time.Time         `json:"timestamp"`
	ManualOverride   bool              `json:"manual_override,omitempty"`
}

// Status represents the status of a single workflow
type Status struct {
	Name       string `json:"name"`
	Status     string `json:"status"`      // in_progress, completed, queued, etc.
	Conclusion string `json:"conclusion"` // success, failure, neutral, cancelled, skipped, etc.
	RunID      int64  `json:"run_id"`
	URL        string `json:"url"`
}

// GateChecker checks GitHub Actions CI status before merge
type GateChecker struct {
	client    *http.Client
	owner     string
	repo      string
	token     string
	branch    string
	workflows []string // workflows to check (e.g., "Build", "Lint", "Tests")
	apiURL    string   // optional override for testing
}

// GateOption configures a GateChecker.
type GateOption func(*GateChecker)

// WithAPIURL overrides the GitHub API endpoint (useful for testing).
func WithAPIURL(url string) GateOption {
	return func(gc *GateChecker) {
		gc.apiURL = url
	}
}

// NewGateChecker creates a new CI gate checker
func NewGateChecker(owner, repo, token, branch string, workflows []string, opts ...GateOption) *GateChecker {
	gc := &GateChecker{
		client:    &http.Client{Timeout: 30 * time.Second},
		owner:     owner,
		repo:      repo,
		token:     token,
		branch:    branch,
		workflows: workflows,
	}
	for _, opt := range opts {
		opt(gc)
	}
	return gc
}

// CheckCI verifies that all required workflows have passed
func (gc *GateChecker) CheckCI(ctx context.Context) (*GateStatus, error) {
	gs := &GateStatus{
		WorkflowStatuses: make(map[string]Status),
		Timestamp:        time.Now(),
	}

	// Default workflows to check if none specified
	workflows := gc.workflows
	if len(workflows) == 0 {
		workflows = []string{"Build", "Lint", "Tests"}
	}

	// Fetch workflow runs from GitHub API
	runs, err := gc.fetchWorkflowRuns(ctx)
	if err != nil {
		return gs, fmt.Errorf("failed to fetch workflow runs: %w", err)
	}

	// Group runs by workflow name, keeping the latest (highest RunID) for each
	workflowMap := make(map[string]Status)
	for _, run := range runs {
		if existing, exists := workflowMap[run.Name]; !exists || run.RunID > existing.RunID {
			workflowMap[run.Name] = run
		}
	}

	// Check each required workflow
	allPassed := true
	for _, workflowName := range workflows {
		run, exists := workflowMap[workflowName]
		if !exists {
			gs.WorkflowStatuses[workflowName] = Status{
				Name:       workflowName,
				Status:     "not_found",
				Conclusion: "failure",
			}
			allPassed = false
			continue
		}

		gs.WorkflowStatuses[workflowName] = run

		// Check if workflow is still in progress
		if run.Status == "in_progress" || run.Status == "queued" {
			allPassed = false
			continue
		}

		// Check if workflow completed successfully
		if run.Status == "completed" && run.Conclusion != "success" {
			allPassed = false
			continue
		}
	}

	gs.Passed = allPassed
	if allPassed {
		gs.Reason = "All required workflows passed"
	} else {
		gs.Reason = "One or more workflows failed or are still in progress"
	}

	return gs, nil
}

// fetchWorkflowRuns fetches the latest workflow runs from GitHub API
func (gc *GateChecker) fetchWorkflowRuns(ctx context.Context) ([]Status, error) {
	apiEndpoint := gc.apiURL
	if apiEndpoint == "" {
		apiEndpoint = fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs?branch=%s&per_page=50", gc.owner, gc.repo, url.QueryEscape(gc.branch))
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create GitHub API request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if gc.token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", gc.token))
	}

	resp, err := gc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var result struct {
		WorkflowRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HtmlURL    string `json:"html_url"`
		} `json:"workflow_runs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse GitHub API response: %w", err)
	}

	var runs []Status
	for _, wr := range result.WorkflowRuns {
		runs = append(runs, Status{
			Name:       wr.Name,
			Status:     wr.Status,
			Conclusion: wr.Conclusion,
			RunID:      wr.ID,
			URL:        wr.HtmlURL,
		})
	}

	return runs, nil
}
