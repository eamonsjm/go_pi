package skill

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LoadEmbed discovers skills from an embedded filesystem. It walks the FS for
// .md files, parses frontmatter only (body is loaded lazily), and returns skills
// with Source set to "built-in".
func LoadEmbed(_ context.Context, fsys fs.FS) ([]*Skill, error) {
	var skills []*Skill

	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("reading embedded skill %s: %w", path, err)
		}

		s, err := ParseFrontmatterOnly(data)
		if err != nil {
			return fmt.Errorf("parsing embedded skill %s: %w", path, err)
		}

		s.Source = "built-in"
		s.SetEmbedSource(fsys, path)
		skills = append(skills, s)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking embedded skills: %w", err)
	}

	return skills, nil
}

// LoadDir discovers skills from a directory on disk. It scans for .md files
// (non-recursive), parses frontmatter only (body is loaded lazily), and returns
// skills with Source set to the given source label.
//
// If the directory does not exist, LoadDir returns nil, nil (not an error).
func LoadDir(_ context.Context, dir, source string) ([]*Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading skill directory %s: %w", dir, err)
	}

	var skills []*Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading skill file %s: %w", path, err)
		}

		s, err := ParseFrontmatterOnly(data)
		if err != nil {
			return nil, fmt.Errorf("parsing skill file %s: %w", path, err)
		}

		s.Source = source
		s.Path = path
		skills = append(skills, s)
	}

	return skills, nil
}

// LoadAll loads skills from all three tiers in override order:
// built-in (embed) < user (~/.gi/skills/) < project (.gi/skills/).
// Skills are registered into the provided registry with last-write-wins semantics.
func LoadAll(ctx context.Context, reg *Registry, embedFS fs.FS) error {
	// Tier 1: built-in skills from embedded filesystem.
	if embedFS != nil {
		builtins, err := LoadEmbed(ctx, embedFS)
		if err != nil {
			return fmt.Errorf("loading built-in skills: %w", err)
		}
		for _, s := range builtins {
			reg.Register(s)
		}
	}

	// Tier 2: user skills from ~/.gi/skills/.
	home, err := os.UserHomeDir()
	if err == nil {
		userDir := filepath.Join(home, ".gi", "skills")
		userSkills, err := LoadDir(ctx, userDir, "user")
		if err != nil {
			return fmt.Errorf("loading user skills: %w", err)
		}
		for _, s := range userSkills {
			reg.Register(s)
		}
	}

	// Tier 3: project skills from .gi/skills/ (relative to cwd).
	cwd, err := os.Getwd()
	if err == nil {
		projectDir := filepath.Join(cwd, ".gi", "skills")
		projectSkills, err := LoadDir(ctx, projectDir, "project")
		if err != nil {
			return fmt.Errorf("loading project skills: %w", err)
		}
		for _, s := range projectSkills {
			reg.Register(s)
		}
	}

	return nil
}
