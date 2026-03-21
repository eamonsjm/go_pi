package skill

import (
	"context"
	"fmt"
	"strings"
)

// SkillTool is a tool that allows the LLM to invoke skills by name.
// It satisfies the tools.Tool interface (Name, Description, Schema, Execute).
type SkillTool struct {
	registry *Registry
	model    string
}

// NewSkillTool creates a new Skill tool backed by the given registry.
func NewSkillTool(reg *Registry, model string) *SkillTool {
	return &SkillTool{registry: reg, model: model}
}

// Name returns the tool name.
func (t *SkillTool) Name() string { return "Skill" }

// Description returns a human-readable description of the tool.
func (t *SkillTool) Description() string {
	return "Execute a skill within the main conversation\n\nWhen users ask you to perform tasks, check if any of the available skills match. Skills provide specialized capabilities and domain knowledge.\n\nWhen users reference a \"slash command\" or \"/<something>\" (e.g., \"/commit\", \"/review-pr\"), they are referring to a skill. Use this tool to invoke it.\n\nHow to invoke:\n- Use this tool with the skill name and optional arguments\n- Examples:\n  - `skill: \"pdf\"` - invoke the pdf skill\n  - `skill: \"commit\", args: \"-m 'Fix bug'\"` - invoke with arguments\n  - `skill: \"review-pr\", args: \"123\"` - invoke with arguments\n\nImportant:\n- Available skills are listed in system-reminder messages in the conversation\n- When a skill matches the user's request, invoke the relevant Skill tool BEFORE generating any other response about the task\n- NEVER mention a skill without actually calling this tool\n- Do not invoke a skill that is already running\n- Do not use this tool for built-in CLI commands (like /help, /clear, etc.)\n- If you see a <command-name> tag in the current conversation turn, the skill has ALREADY been loaded - follow the instructions directly instead of calling this tool again"
}

// Schema returns the JSON Schema for the tool's input parameters.
func (t *SkillTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill": map[string]any{
				"type":        "string",
				"description": "The skill name. E.g., \"commit\", \"review-pr\", or \"pdf\"",
			},
			"args": map[string]any{
				"type":        "string",
				"description": "Optional arguments for the skill",
			},
		},
		"required": []string{"skill"},
	}
}

// Execute invokes the named skill, rendering its template with the given arguments.
func (t *SkillTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	name, _ := params["skill"].(string)
	if name == "" {
		return "", fmt.Errorf("missing required parameter: skill")
	}

	args, _ := params["args"].(string)

	s, ok := t.registry.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown skill: %q. Available skills: %s", name, strings.Join(t.registry.Names(), ", "))
	}

	body, err := s.LoadBody()
	if err != nil {
		return "", fmt.Errorf("failed to load skill %q: %w", name, err)
	}

	// Parse arguments against skill definitions.
	argVars, err := ParseSkillArgs(s.Arguments, args)
	if err != nil {
		return "", fmt.Errorf("skill %q argument error: %w", name, err)
	}

	// Merge context vars (cwd, branch, model) with argument vars.
	vars := ContextVars(t.model)
	for k, v := range argVars {
		vars[k] = v
	}

	return RenderTemplate(body, vars), nil
}

// SetModel updates the model used for context variables in template rendering.
func (t *SkillTool) SetModel(model string) {
	t.model = model
}

// SkillSystemReminder generates a compact skill index suitable for injection
// as a system-reminder block. Only user-invocable skills are included.
// Returns empty string if no user-invocable skills exist.
func SkillSystemReminder(reg *Registry) string {
	skills := reg.UserInvocable()
	if len(skills) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("The following skills are available for use with the Skill tool:\n\n")
	for _, s := range skills {
		desc := s.Description
		if s.Trigger != "" {
			desc += "\nTRIGGER when: " + s.Trigger
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, desc))
	}
	return b.String()
}
