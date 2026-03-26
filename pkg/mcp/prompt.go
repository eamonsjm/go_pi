package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/ejm/go_pi/pkg/skill"
)

// PromptArgument preserves the required/optional distinction from MCP prompts.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptInfo is the wire format of a prompt from prompts/list.
type PromptInfo struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptsListPage is the response from prompts/list.
type PromptsListPage struct {
	Prompts    []PromptInfo `json:"prompts"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// PromptMessage is a single message returned by prompts/get.
type PromptMessage struct {
	Role    string         `json:"role"`
	Content ContentItem `json:"content"`
}

// PromptsGetResult is the response from prompts/get.
type PromptsGetResult struct {
	Description string             `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptGetter can execute a prompts/get RPC on an MCP server.
// Server implements this; defined as an interface for testability.
type PromptGetter interface {
	ServerName() string
	GetPrompt(ctx context.Context, name string, arguments map[string]string) (*PromptsGetResult, error)
}

// BridgePrompt creates a skill.Skill from an PromptInfo, bridging MCP
// prompts into the skill registry. The skill's Source is "mcp" and
// UserInvocable is true so it appears in the skill list.
//
// When getter is non-nil, the skill's Executor calls prompts/get on the
// MCP server and returns the rendered messages. When getter is nil (e.g.,
// in tests that only check metadata), the skill falls back to a static body.
func BridgePrompt(serverName string, info PromptInfo, getter PromptGetter) *skill.Skill {
	name := "mcp__" + serverName + "__" + info.Name

	// Convert MCP arguments to skill arguments.
	var args []skill.Argument
	for _, a := range info.Arguments {
		args = append(args, skill.Argument{
			Name:        a.Name,
			Description: a.Description,
			Required:    a.Required,
		})
	}

	s := &skill.Skill{
		Name:          name,
		Description:   info.Description,
		UserInvocable: true,
		Arguments:     args,
		Source:         "mcp",
		Body:          fmt.Sprintf("[MCP prompt from server %q — invoke via Skill tool]", serverName),
	}

	if getter != nil {
		promptName := info.Name
		mcpArgs := info.Arguments
		s.Executor = func(ctx context.Context, argVars map[string]string) (string, error) {
			// Validate required arguments before the RPC.
			if err := ValidatePromptArgs(mcpArgs, argVars); err != nil {
				return "", err
			}
			result, err := getter.GetPrompt(ctx, promptName, argVars)
			if err != nil {
				return "", fmt.Errorf("prompts/get %q on server %q: %w", promptName, serverName, err)
			}
			return formatPromptResult(result), nil
		}
	}

	return s
}

// formatPromptResult renders a PromptsGetResult as a string suitable for
// returning to the LLM. Each message is formatted with its role prefix.
func formatPromptResult(result *PromptsGetResult) string {
	var b strings.Builder
	if result.Description != "" {
		b.WriteString(result.Description)
		b.WriteString("\n\n")
	}
	for i, msg := range result.Messages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s]\n%s", msg.Role, msg.Content.Text)
	}
	return b.String()
}

// DiscoverPrompts fetches all prompts from an MCP server via paginated
// prompts/list and returns them as skill.Skill entries ready for registration.
// The getter is passed to BridgePrompt so each skill can execute prompts/get.
func DiscoverPrompts(
	ctx context.Context,
	serverName string,
	listPrompts func(ctx context.Context, cursor string) (*PromptsListPage, error),
	getter PromptGetter,
) ([]*skill.Skill, error) {
	var allSkills []*skill.Skill
	var cursor string
	for pages := 0; pages < maxPaginationPages; pages++ {
		page, err := listPrompts(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("prompts/list (page %d): %w", pages, err)
		}
		for _, info := range page.Prompts {
			allSkills = append(allSkills, BridgePrompt(serverName, info, getter))
		}
		if page.NextCursor == "" || len(allSkills) >= maxTotalItems {
			break
		}
		cursor = page.NextCursor
	}
	return allSkills, nil
}

// ListPrompts sends a prompts/list request via the MCP client with pagination.
func (c *Client) ListPrompts(ctx context.Context, cursor string) (*PromptsListPage, error) {
	params := map[string]any{}
	if cursor != "" {
		params["cursor"] = cursor
	}
	result, err := c.Request(ctx, "prompts/list", params)
	if err != nil {
		return nil, fmt.Errorf("prompts/list: %w", err)
	}
	var page PromptsListPage
	if err := json.Unmarshal(result, &page); err != nil {
		return nil, fmt.Errorf("parsing prompts/list response: %w", err)
	}
	return &page, nil
}

// GetPrompt sends a prompts/get request for a specific prompt with arguments.
func (c *Client) GetPrompt(ctx context.Context, name string, arguments map[string]string) (*PromptsGetResult, error) {
	params := map[string]any{
		"name": name,
	}
	if len(arguments) > 0 {
		params["arguments"] = arguments
	}
	result, err := c.Request(ctx, "prompts/get", params)
	if err != nil {
		return nil, fmt.Errorf("prompts/get %q: %w", name, err)
	}
	var promptResult PromptsGetResult
	if err := json.Unmarshal(result, &promptResult); err != nil {
		return nil, fmt.Errorf("parsing prompts/get response: %w", err)
	}
	return &promptResult, nil
}

// ValidatePromptArgs checks that all required arguments are present.
// Returns an error listing missing required arguments.
func ValidatePromptArgs(args []PromptArgument, provided map[string]string) error {
	var missing []string
	for _, a := range args {
		if a.Required {
			if _, ok := provided[a.Name]; !ok {
				missing = append(missing, a.Name)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required argument(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// GetPrompt implements PromptGetter by delegating to the MCP client.
func (s *Server) GetPrompt(ctx context.Context, name string, arguments map[string]string) (*PromptsGetResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrServerCrashed
	}
	s.mu.Unlock()

	return s.client.GetPrompt(ctx, name, arguments)
}

// discoverAndRegisterPrompts discovers prompts and registers them in the skill registry.
func (s *Server) discoverAndRegisterPrompts(ctx context.Context) error {
	if s.manager.skillRegistry == nil {
		return nil
	}

	discovered, err := DiscoverPrompts(ctx, s.name, func(ctx context.Context, cursor string) (*PromptsListPage, error) {
		return s.client.ListPrompts(ctx, cursor)
	}, s)
	if err != nil {
		return err
	}

	prefix := "mcp__" + s.name + "__"
	s.manager.skillRegistry.ReplaceByPrefix(prefix, discovered)

	log.Printf("mcp: server %q: registered %d prompts", s.name, len(discovered))
	return nil
}

// diffSkillCount returns the number of skills added and removed between old and new slices.
func diffSkillCount(old, cur []*skill.Skill) (added, removed int) {
	oldNames := make(map[string]struct{}, len(old))
	for _, s := range old {
		oldNames[s.Name] = struct{}{}
	}
	newNames := make(map[string]struct{}, len(cur))
	for _, s := range cur {
		newNames[s.Name] = struct{}{}
	}
	for n := range newNames {
		if _, ok := oldNames[n]; !ok {
			added++
		}
	}
	for n := range oldNames {
		if _, ok := newNames[n]; !ok {
			removed++
		}
	}
	return
}
