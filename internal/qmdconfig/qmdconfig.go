// Package qmdconfig locates and parses QMD's index.yml. It never writes to it.
package qmdconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPattern is QMD's glob mask when a collection sets none.
const DefaultPattern = "**/*.md"

// Config is the subset of index.yml that lantern needs. Unknown keys are ignored.
type Config struct {
	GlobalContext string `yaml:"global_context"`
	EditorURI     string `yaml:"editor_uri"`
	// EditorURITemplate is QMD's accepted alias for editor_uri.
	EditorURITemplate string                 `yaml:"editor_uri_template"`
	Collections       map[string]*Collection `yaml:"collections"`

	// Path is the file this config was loaded from.
	Path string `yaml:"-"`
}

// Collection is one entry under `collections:`.
type Collection struct {
	Name             string            `yaml:"-"`
	Path             string            `yaml:"path"`
	Pattern          string            `yaml:"pattern"`
	Ignore           []string          `yaml:"ignore"`
	IncludeByDefault *bool             `yaml:"includeByDefault"`
	Context          map[string]string `yaml:"context"`
}

// Env abstracts environment lookups so path resolution is testable.
type Env func(key string) string

// Locate returns the index.yml path, resolved the same way QMD does:
// explicit flag, then $QMD_CONFIG_DIR, then $XDG_CONFIG_HOME/qmd, then ~/.config/qmd.
// A non-empty index name selects "<name>.yml" instead of "index.yml".
func Locate(flagPath, indexName string, env Env, home string) string {
	if flagPath != "" {
		return expandHome(flagPath, home)
	}
	file := "index.yml"
	if indexName != "" {
		file = indexName + ".yml"
	}
	if dir := env("QMD_CONFIG_DIR"); dir != "" {
		return filepath.Join(expandHome(dir, home), file)
	}
	if dir := env("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(expandHome(dir, home), "qmd", file)
	}
	return filepath.Join(home, ".config", "qmd", file)
}

// Load reads and parses the config at path, filling in QMD's defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read qmd config: %w", err)
	}
	home, _ := os.UserHomeDir()
	cfg, err := Parse(data, home)
	if err != nil {
		return nil, fmt.Errorf("parse qmd config %s: %w", path, err)
	}
	cfg.Path = path
	return cfg, nil
}

// Parse parses index.yml content. home is used to expand a leading "~" in collection paths.
func Parse(data []byte, home string) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.EditorURI == "" {
		cfg.EditorURI = cfg.EditorURITemplate
	}
	if cfg.Collections == nil {
		cfg.Collections = map[string]*Collection{}
	}
	for name, c := range cfg.Collections {
		if c == nil {
			c = &Collection{}
			cfg.Collections[name] = c
		}
		c.Name = name
		c.Path = filepath.Clean(expandHome(c.Path, home))
		if strings.TrimSpace(c.Pattern) == "" {
			c.Pattern = DefaultPattern
		}
	}
	return &cfg, nil
}

// Names returns the collection names, sorted.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Collections))
	for n := range c.Collections {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Sorted returns the collections ordered by name.
func (c *Config) Sorted() []*Collection {
	out := make([]*Collection, 0, len(c.Collections))
	for _, n := range c.Names() {
		out = append(out, c.Collections[n])
	}
	return out
}

// Included reports whether unscoped queries search this collection (default true).
func (c *Collection) Included() bool {
	return c.IncludeByDefault == nil || *c.IncludeByDefault
}

// Patterns splits the glob mask into its union members. QMD accepts both
// comma-separated lists and brace groups; commas inside braces are kept.
func (c *Collection) Patterns() []string {
	var out []string
	depth, start := 0, 0
	p := c.Pattern
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = appendPattern(out, p[start:i])
				start = i + 1
			}
		}
	}
	out = appendPattern(out, p[start:])
	if len(out) == 0 {
		out = []string{DefaultPattern}
	}
	return out
}

func appendPattern(out []string, s string) []string {
	if s = strings.TrimSpace(s); s != "" {
		out = append(out, s)
	}
	return out
}

// ContextFor returns the description for a path inside the collection, using the
// longest matching context prefix. Keys may be written "/work" or "work"; the
// collection root may be "", "/" or ".". rel is slash-separated and relative to
// the collection root (a folder or a file).
func (c *Collection) ContextFor(rel string) string {
	rel = normalizePrefix(rel)
	best, bestLen := "", -1
	for k, v := range c.Context {
		key := normalizePrefix(k)
		if !hasPathPrefix(rel, key) {
			continue
		}
		if len(key) > bestLen {
			best, bestLen = v, len(key)
		}
	}
	return best
}

// RootContext is the description attached to the collection root, if any.
func (c *Collection) RootContext() string {
	return c.ContextFor("")
}

func normalizePrefix(p string) string {
	p = strings.Trim(filepath.ToSlash(p), "/")
	if p == "." {
		return ""
	}
	return p
}

// hasPathPrefix reports whether prefix is rel itself or one of its parent folders.
func hasPathPrefix(rel, prefix string) bool {
	if prefix == "" || rel == prefix {
		return true
	}
	return strings.HasPrefix(rel, prefix+"/")
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
