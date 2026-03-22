package skill

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// RenderTemplate performs variable substitution and conditional block evaluation
// on a skill body template.
//
// Variable substitution: {{var}} is replaced with the value from vars.
// Undefined variables are left as-is (forward-compatible with Go text/template).
//
// Conditional blocks: {{#if var}}...{{/if}} includes the inner content only when
// var is present and non-empty in vars. Conditionals do not nest.
func RenderTemplate(body string, vars map[string]string) string {
	// Process conditional blocks first.
	body = evalConditionals(body, vars)

	// Then substitute variables.
	body = substituteVars(body, vars)

	return body
}

// ifBlockRe matches {{#if var}}...{{/if}} blocks (non-greedy, non-nesting).
var ifBlockRe = regexp.MustCompile(`\{\{#if\s+(\w+)\}\}([\s\S]*?)\{\{/if\}\}`)

// evalConditionals processes all {{#if var}}...{{/if}} blocks.
func evalConditionals(body string, vars map[string]string) string {
	return ifBlockRe.ReplaceAllStringFunc(body, func(match string) string {
		sub := ifBlockRe.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		varName := sub[1]
		inner := sub[2]
		if v, ok := vars[varName]; ok && v != "" {
			return inner
		}
		return ""
	})
}

// varRe matches {{varname}} where varname is word characters (letters, digits, underscore).
var varRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

// substituteVars replaces {{var}} with the corresponding value from vars.
// Undefined variables are left as-is.
func substituteVars(body string, vars map[string]string) string {
	return varRe.ReplaceAllStringFunc(body, func(match string) string {
		sub := varRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		name := sub[1]
		if v, ok := vars[name]; ok {
			return v
		}
		return match // leave undefined vars untouched
	})
}

// ContextVars returns the standard context variables: cwd, branch, model.
// The model parameter is the currently active model name.
// The context is used to bound the git branch lookup with a timeout.
func ContextVars(ctx context.Context, model string) map[string]string {
	vars := make(map[string]string, 3)

	if cwd, err := os.Getwd(); err == nil {
		vars["cwd"] = cwd
	}

	if branch, err := gitBranch(ctx); err == nil {
		vars["branch"] = branch
	}

	if model != "" {
		vars["model"] = model
	}

	return vars
}

// gitBranchTimeout bounds how long we wait for git rev-parse before giving up.
const gitBranchTimeout = 3 * time.Second

// gitBranch returns the current git branch name via git rev-parse.
// It uses a derived context with a short timeout so a hung git process
// (e.g. NFS stall, locked index) cannot block indefinitely.
func gitBranch(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitBranchTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ParseSkillArgs parses a raw argument string against the skill's argument
// definitions. It supports:
//   - Positional arguments: matched in order to argument definitions
//   - Named flags: -name value or -name=value
//
// Returns a map of argument name to value. Returns an error if a required
// argument is missing.
func ParseSkillArgs(defs []Argument, raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	result := make(map[string]string, len(defs))

	if raw == "" {
		// Check for missing required args.
		for _, d := range defs {
			if d.Required {
				return nil, fmt.Errorf("missing required argument: %s", d.Name)
			}
		}
		return result, nil
	}

	tokens := tokenize(raw)

	// Build a set of known argument names for flag detection.
	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Name] = true
	}

	// First pass: extract named flags (-name value or -name=value).
	var positional []string
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if strings.HasPrefix(tok, "-") {
			flagPart := strings.TrimLeft(tok, "-")
			if flagPart == "" {
				// Bare "-" or "--" — treat as positional.
				positional = append(positional, tok)
				continue
			}

			// Check for -name=value form.
			if eqIdx := strings.IndexByte(flagPart, '='); eqIdx >= 0 {
				name := flagPart[:eqIdx]
				val := flagPart[eqIdx+1:]
				if defNames[name] {
					result[name] = val
					continue
				}
			}

			// -name value form.
			name := flagPart
			if defNames[name] && i+1 < len(tokens) {
				i++
				result[name] = tokens[i]
				continue
			}

			// Unknown flag — treat as positional.
			positional = append(positional, tok)
		} else {
			positional = append(positional, tok)
		}
	}

	// Second pass: assign positional arguments to definitions that
	// weren't already filled by named flags.
	posIdx := 0
	for _, d := range defs {
		if _, ok := result[d.Name]; ok {
			continue // already set by flag
		}
		if posIdx < len(positional) {
			result[d.Name] = positional[posIdx]
			posIdx++
		}
	}

	// Remaining positional tokens (beyond defined args) get joined into
	// the last defined argument if there is one, to support variadic-like usage.
	if posIdx < len(positional) && len(defs) > 0 {
		last := defs[len(defs)-1]
		existing := result[last.Name]
		remaining := strings.Join(positional[posIdx:], " ")
		if existing != "" {
			result[last.Name] = existing + " " + remaining
		} else {
			result[last.Name] = remaining
		}
	}

	// Check for missing required args.
	for _, d := range defs {
		if d.Required {
			if v, ok := result[d.Name]; !ok || v == "" {
				return nil, fmt.Errorf("missing required argument: %s", d.Name)
			}
		}
	}

	return result, nil
}

// tokenize splits a raw argument string into tokens, respecting quoted strings.
// Both single and double quotes are supported. Quotes are stripped from the result.
func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	var quoteChar byte

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inQuote:
			if ch == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(ch)
			}
		case ch == '"' || ch == '\'':
			inQuote = true
			quoteChar = ch
		case ch == ' ' || ch == '\t':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}
