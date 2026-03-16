package tui

import (
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
)

func TestScanMessagesForSecrets_NoSecrets(t *testing.T) {
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "hello world"),
		ai.NewTextMessage(ai.RoleAssistant, "hi there"),
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "file.go\nmain.go\nREADME.md"},
		}},
	}

	findings := scanMessagesForSecrets(msgs)
	if len(findings) != 0 {
		t.Errorf("expected no findings, got %d: %v", len(findings), findings)
	}
}

func TestScanMessagesForSecrets_AWSKey(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE"},
		}},
	}

	findings := scanMessagesForSecrets(msgs)
	if len(findings) == 0 {
		t.Fatal("expected to detect AWS key")
	}

	found := false
	for _, f := range findings {
		if f.PatternName == "AWS access key" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected AWS access key finding, got: %v", findings)
	}
}

func TestScanMessagesForSecrets_GitHubToken(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "GITHUB_TOKEN=ghp_ABCDEFGHIJKLMNOPQRSTuvwxyz123456"},
		}},
	}

	findings := scanMessagesForSecrets(msgs)
	if len(findings) == 0 {
		t.Fatal("expected to detect GitHub token")
	}

	found := false
	for _, f := range findings {
		if f.PatternName == "GitHub token" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected GitHub token finding, got: %v", findings)
	}
}

func TestScanMessagesForSecrets_PrivateKey(t *testing.T) {
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleAssistant, "Here is the key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEo..."),
	}

	findings := scanMessagesForSecrets(msgs)
	if len(findings) == 0 {
		t.Fatal("expected to detect private key")
	}
}

func TestScanMessagesForSecrets_Password(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "DB_PASSWORD=supersecretpassword123"},
		}},
	}

	findings := scanMessagesForSecrets(msgs)
	if len(findings) == 0 {
		t.Fatal("expected to detect password")
	}
}

func TestScanMessagesForSecrets_ConnectionString(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "DATABASE_URL=postgres://admin:s3cret@db.example.com:5432/mydb"},
		}},
	}

	findings := scanMessagesForSecrets(msgs)
	if len(findings) == 0 {
		t.Fatal("expected to detect connection string with credentials")
	}
}

func TestScanMessagesForSecrets_BearerToken(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N"},
		}},
	}

	findings := scanMessagesForSecrets(msgs)
	if len(findings) == 0 {
		t.Fatal("expected to detect bearer token")
	}
}

func TestScanMessagesForSecrets_StripeKey(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "STRIPE_KEY=sk_test_4eC39HqLyjWDarjtT1zdp7dc"},
		}},
	}

	findings := scanMessagesForSecrets(msgs)
	if len(findings) == 0 {
		t.Fatal("expected to detect Stripe key")
	}
}

func TestScanMessagesForSecrets_SlackToken(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "SLACK_TOKEN=xoxb-123456789012-1234567890123-AbCdEfGhIjKlMnOpQrStUvWx"},
		}},
	}

	findings := scanMessagesForSecrets(msgs)
	if len(findings) == 0 {
		t.Fatal("expected to detect Slack token")
	}
}

func TestScanMessagesForSecrets_IgnoresToolUse(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolName: "read", Input: "AKIAIOSFODNN7EXAMPLE"},
		}},
	}

	findings := scanMessagesForSecrets(msgs)
	// Tool use input is scanned via text/tool_result only; direct tool_use blocks
	// may contain file paths, not secrets typically. The scanner focuses on
	// tool_result (file contents) and text.
	if len(findings) != 0 {
		t.Errorf("expected no findings from tool_use block, got %d", len(findings))
	}
}

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string // must appear in output
		excludes string // must NOT appear in output
	}{
		{
			name:     "AWS key",
			input:    "key: AKIAIOSFODNN7EXAMPLE",
			contains: "[REDACTED]",
			excludes: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:     "password value",
			input:    "password=mysecretpassword",
			contains: "password=[REDACTED]",
			excludes: "mysecretpassword",
		},
		{
			name:     "API key with quotes",
			input:    `api_key: "sk_abcdefghijklmnop1234"`,
			contains: "[REDACTED]",
		},
		{
			name:     "private key header",
			input:    "-----BEGIN RSA PRIVATE KEY-----",
			contains: "[REDACTED]",
			excludes: "BEGIN RSA PRIVATE KEY",
		},
		{
			name:     "no secrets",
			input:    "just normal text here",
			contains: "just normal text here",
		},
		{
			name:     "GitHub token",
			input:    "token=ghp_ABCDEFGHIJKLMNOPQRSTuvwx",
			contains: "[REDACTED]",
			excludes: "ghp_ABCDEFGHIJKLMNOPQRST",
		},
		{
			name:     "connection string",
			input:    "postgres://user:pass@host:5432/db",
			contains: "[REDACTED]",
			excludes: "user:pass@",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := redactSecrets(tt.input)
			if tt.contains != "" && !strings.Contains(result, tt.contains) {
				t.Errorf("expected result to contain %q, got %q", tt.contains, result)
			}
			if tt.excludes != "" && strings.Contains(result, tt.excludes) {
				t.Errorf("expected result to NOT contain %q, got %q", tt.excludes, result)
			}
		})
	}
}

func TestRedactSessionMessages(t *testing.T) {
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "read .env"),
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "API_KEY=AKIAIOSFODNN7EXAMPLE\nDB_HOST=localhost"},
		}},
		ai.NewTextMessage(ai.RoleAssistant, "I see the API key is AKIAIOSFODNN7EXAMPLE"),
	}

	sanitized := redactSessionMessages(msgs)

	// Original should be unchanged
	if msgs[1].Content[0].Content != "API_KEY=AKIAIOSFODNN7EXAMPLE\nDB_HOST=localhost" {
		t.Error("original message was mutated")
	}

	// Tool result should be redacted
	if strings.Contains(sanitized[1].Content[0].Content, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("tool result should have AWS key redacted")
	}

	// Assistant text should be redacted
	if strings.Contains(sanitized[2].Content[0].Text, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("assistant text should have AWS key redacted")
	}

	// Non-secret content should survive
	if !strings.Contains(sanitized[1].Content[0].Content, "DB_HOST=localhost") {
		t.Error("non-secret content should be preserved")
	}
}

func TestFormatSecretWarning(t *testing.T) {
	findings := []secretFinding{
		{PatternName: "AWS access key", Context: "AKIA..."},
		{PatternName: "password", Context: "password=..."},
		{PatternName: "password", Context: "db_password=..."},
	}

	warning := formatSecretWarning(findings)

	if !strings.Contains(warning, "Potential secrets detected") {
		t.Error("warning should mention potential secrets")
	}
	if !strings.Contains(warning, "AWS access key") {
		t.Error("warning should list AWS access key")
	}
	if !strings.Contains(warning, "password (2 matches)") {
		t.Error("warning should group and count password matches")
	}
	if !strings.Contains(warning, "--force") {
		t.Error("warning should mention --force flag")
	}
	if !strings.Contains(warning, "anyone with the URL") {
		t.Error("warning should mention gist URL visibility")
	}
}

func TestExcerptAround(t *testing.T) {
	text := "some prefix API_KEY=AKIAIOSFODNN7EXAMPLE some suffix"
	// Find the AWS key position
	start := strings.Index(text, "AKIA")
	end := start + 20 // AKIAIOSFODNN7EXAMPLE

	excerpt := excerptAround(text, start, end)
	if excerpt == "" {
		t.Error("excerpt should not be empty")
	}
	if len(excerpt) > 80 {
		t.Error("excerpt should be at most 80 characters")
	}
}
