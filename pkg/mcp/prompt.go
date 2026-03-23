package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/ejm/go_pi/pkg/skill"
)

// MCPPromptArgument preserves the required/optional distinction from MCP prompts.
type MCPPromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// MCPPromptInfo is the wire format of a prompt from prompts/list.
type MCPPromptInfo struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Arguments   []MCPPromptArgument `json:"arguments,omitempty"`
}

// PromptsListPage is the response from prompts/list.
type PromptsListPage struct {
	Prompts    []MCPPromptInfo `json:"prompts"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// MCPPromptMessage is a single message returned by prompts/get.
type MCPPromptMessage struct {
	Role    string         `json:"role"`
	Content MCPContentItem `json:"content"`
}

// PromptsGetResult is the response from prompts/get.
type PromptsGetResult struct {
	Description string             `json:"description,omitempty"`
	Messages    []MCPPromptMessage `json:"messages"`
}

// BridgePrompt creates a skill.Skill from an MCPPromptInfo, bridging MCP
// prompts into the skill registry. The skill's Source is "mcp" and
// UserInvocable is true so it appears in the skill list.
func BridgePrompt(serverName string, info MCPPromptInfo) *skill.Skill {
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

	return &skill.Skill{
		Name:          name,
		Description:   info.Description,
		UserInvocable: true,
		Arguments:     args,
		Source:         "mcp",
		Body:          fmt.Sprintf("[MCP prompt from server %q — invoke via Skill tool]", serverName),
	}
}

// DiscoverPrompts fetches all prompts from an MCP server via paginated
// prompts/list and returns them as skill.Skill entries ready for registration.
func DiscoverPrompts(
	ctx context.Context,
	serverName string,
	listPrompts func(ctx context.Context, cursor string) (*PromptsListPage, error),
) ([]*skill.Skill, error) {
	var allSkills []*skill.Skill
	var cursor string
	for pages := 0; pages < maxPaginationPages; pages++ {
		page, err := listPrompts(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("prompts/list (page %d): %w", pages, err)
		}
		for _, info := range page.Prompts {
			allSkills = append(allSkills, BridgePrompt(serverName, info))
		}
		if page.NextCursor == "" || len(allSkills) >= maxTotalItems {
			break
		}
		cursor = page.NextCursor
	}
	return allSkills, nil
}

// ListPrompts sends a prompts/list request via the MCP client with pagination.
func (c *MCPClient) ListPrompts(ctx context.Context, cursor string) (*PromptsListPage, error) {
	params := map[string]any{}
	if cursor != "" {
		params["cursor"] = cursor
	}
	result, err := c.Request(ctx, "prompts/list", params)
	if err != nil {
		return nil, err
	}
	var page PromptsListPage
	if err := json.Unmarshal(result, &page); err != nil {
		return nil, fmt.Errorf("parsing prompts/list response: %w", err)
	}
	return &page, nil
}

// GetPrompt sends a prompts/get request for a specific prompt with arguments.
func (c *MCPClient) GetPrompt(ctx context.Context, name string, arguments map[string]string) (*PromptsGetResult, error) {
	params := map[string]any{
		"name": name,
	}
	if len(arguments) > 0 {
		params["arguments"] = arguments
	}
	result, err := c.Request(ctx, "prompts/get", params)
	if err != nil {
		return nil, err
	}
	var promptResult PromptsGetResult
	if err := json.Unmarshal(result, &promptResult); err != nil {
		return nil, fmt.Errorf("parsing prompts/get response: %w", err)
	}
	return &promptResult, nil
}

// ValidatePromptArgs checks that all required arguments are present.
// Returns an error listing missing required arguments.
func ValidatePromptArgs(args []MCPPromptArgument, provided map[string]string) error {
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

// discoverAndRegisterPrompts discovers prompts and registers them in the skill registry.
func (s *MCPServer) discoverAndRegisterPrompts(ctx context.Context) error {
	if s.manager.skillRegistry == nil {
		return nil
	}

	discovered, err := DiscoverPrompts(ctx, s.name, func(ctx context.Context, cursor string) (*PromptsListPage, error) {
		return s.client.ListPrompts(ctx, cursor)
	})
	if err != nil {
		return err
	}

	prefix := "mcp__" + s.name + "__"
	s.manager.skillRegistry.ReplaceByPrefix(prefix, discovered)

	log.Printf("mcp: server %q: registered %d prompts", s.name, len(discovered))
	return nil
}

// diffSkillCount returns the number of skills added and removed between old and new slices.
func diffSkillCount(old, new []*skill.Skill) (added, removed int) {
	oldNames := make(map[string]struct{}, len(old))
	for _, s := range old {
		oldNames[s.Name] = struct{}{}
	}
	newNames := make(map[string]struct{}, len(new))
	for _, s := range new {
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
