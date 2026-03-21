package skill

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Skill is a reusable prompt template invoked as a slash command or via the Skill tool.
type Skill struct {
	Name          string     `yaml:"name"`
	Description   string     `yaml:"description"`
	UserInvocable bool       `yaml:"user_invocable"`
	Arguments     []Argument `yaml:"arguments,omitempty"`
	Trigger       string     `yaml:"trigger,omitempty"`

	// Body is the prompt template (everything after frontmatter).
	// Loaded lazily via LoadBody; empty until first call.
	Body string `yaml:"-"`

	// Source indicates where this skill came from: "built-in", "user", or "project".
	Source string `yaml:"-"`

	// Path is the file path for file-based skills (empty for embed-based before load).
	Path string `yaml:"-"`

	// embedFS and embedPath support lazy loading from an embedded filesystem.
	embedFS   fs.FS
	embedPath string

	bodyOnce sync.Once
	bodyErr  error
}

// Argument defines a named parameter that a skill accepts.
type Argument struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

// LoadBody lazily loads and caches the skill body. For file-based skills it reads
// from Path; for embed-based skills it reads from the embedded FS. The result is
// cached after the first successful call.
func (s *Skill) LoadBody() (string, error) {
	// If Body was already set during parsing (e.g. via ParseSkill with full content),
	// return it directly.
	if s.Body != "" {
		return s.Body, nil
	}

	s.bodyOnce.Do(func() {
		var data []byte

		switch {
		case s.embedFS != nil && s.embedPath != "":
			data, s.bodyErr = fs.ReadFile(s.embedFS, s.embedPath)
		case s.Path != "":
			data, s.bodyErr = os.ReadFile(s.Path)
		default:
			s.bodyErr = fmt.Errorf("skill %q: no source path or embed FS configured", s.Name)
			return
		}
		if s.bodyErr != nil {
			return
		}

		// Re-parse to extract just the body portion.
		_, body, err := splitFrontmatter(data)
		if err != nil {
			s.bodyErr = err
			return
		}
		s.Body = body
	})

	return s.Body, s.bodyErr
}

// SetEmbedSource sets the embedded filesystem and path for lazy body loading.
func (s *Skill) SetEmbedSource(fsys fs.FS, path string) {
	s.embedFS = fsys
	s.embedPath = path
}

// ParseSkill parses a skill definition from raw bytes. It splits on '---' delimiters,
// unmarshals YAML frontmatter, and stores the body separately.
func ParseSkill(data []byte) (*Skill, error) {
	front, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, err
	}

	var s Skill
	if err := yaml.Unmarshal(front, &s); err != nil {
		return nil, fmt.Errorf("parsing skill frontmatter: %w", err)
	}

	if s.Name == "" {
		return nil, fmt.Errorf("skill frontmatter missing required field: name")
	}

	s.Body = body
	return &s, nil
}

// ParseFrontmatterOnly parses only the YAML frontmatter of a skill definition,
// without storing the body. Used for lazy-loading: the body is read on demand later.
func ParseFrontmatterOnly(data []byte) (*Skill, error) {
	front, _, err := splitFrontmatter(data)
	if err != nil {
		return nil, err
	}

	var s Skill
	if err := yaml.Unmarshal(front, &s); err != nil {
		return nil, fmt.Errorf("parsing skill frontmatter: %w", err)
	}

	if s.Name == "" {
		return nil, fmt.Errorf("skill frontmatter missing required field: name")
	}

	return &s, nil
}

// splitFrontmatter splits a markdown document with YAML frontmatter into
// the frontmatter bytes and the body string. The document must start with
// a '---' line and contain a closing '---' line.
func splitFrontmatter(data []byte) (frontmatter []byte, body string, err error) {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return nil, "", fmt.Errorf("skill file does not start with '---' frontmatter delimiter")
	}

	// Find the closing delimiter after the opening one.
	rest := trimmed[3:]
	// Skip the remainder of the opening delimiter line.
	if idx := bytes.IndexByte(rest, '\n'); idx >= 0 {
		rest = rest[idx+1:]
	} else {
		return nil, "", fmt.Errorf("skill file has no content after opening '---'")
	}

	closeIdx := bytes.Index(rest, []byte("\n---"))
	if closeIdx < 0 {
		return nil, "", fmt.Errorf("skill file missing closing '---' frontmatter delimiter")
	}

	frontmatter = rest[:closeIdx]

	// Body starts after the closing "---" line.
	afterClose := rest[closeIdx+4:] // skip "\n---"
	// Skip the rest of the closing delimiter line.
	if idx := bytes.IndexByte(afterClose, '\n'); idx >= 0 {
		afterClose = afterClose[idx+1:]
	} else {
		afterClose = nil
	}

	body = strings.TrimSpace(string(afterClose))
	return frontmatter, body, nil
}

// Registry holds skills indexed by name. Skills registered later with the same
// name overwrite earlier ones, enabling the override chain: built-in < user < project.
type Registry struct {
	skills map[string]*Skill
}

// NewRegistry creates a new empty skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]*Skill),
	}
}

// Register adds a skill to the registry. If a skill with the same name
// already exists, it is replaced (last-write-wins for override semantics).
func (r *Registry) Register(s *Skill) {
	r.skills[s.Name] = s
}

// Get returns the skill with the given name, or false if not found.
func (r *Registry) Get(name string) (*Skill, bool) {
	s, ok := r.skills[name]
	return s, ok
}

// Names returns all registered skill names sorted alphabetically.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// UserInvocable returns all skills with UserInvocable=true, sorted by name.
func (r *Registry) UserInvocable() []*Skill {
	var result []*Skill
	for _, s := range r.skills {
		if s.UserInvocable {
			result = append(result, s)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// All returns all registered skills sorted by name.
func (r *Registry) All() []*Skill {
	result := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Len returns the number of registered skills.
func (r *Registry) Len() int {
	return len(r.skills)
}
