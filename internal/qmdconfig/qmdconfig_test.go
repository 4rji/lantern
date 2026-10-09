package qmdconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func envMap(m map[string]string) Env {
	return func(k string) string { return m[k] }
}

func TestLocate(t *testing.T) {
	home := "/home/me"
	tests := []struct {
		name  string
		flag  string
		index string
		env   map[string]string
		want  string
	}{
		{"default", "", "", nil, "/home/me/.config/qmd/index.yml"},
		{"named index", "", "work", nil, "/home/me/.config/qmd/work.yml"},
		{"xdg", "", "", map[string]string{"XDG_CONFIG_HOME": "/xdg"}, "/xdg/qmd/index.yml"},
		{"qmd dir beats xdg", "", "", map[string]string{"XDG_CONFIG_HOME": "/xdg", "QMD_CONFIG_DIR": "/qmdcfg"}, "/qmdcfg/index.yml"},
		{"qmd dir named", "", "work", map[string]string{"QMD_CONFIG_DIR": "/qmdcfg"}, "/qmdcfg/work.yml"},
		{"flag beats env", "/etc/my.yml", "work", map[string]string{"QMD_CONFIG_DIR": "/qmdcfg"}, "/etc/my.yml"},
		{"flag tilde", "~/q/index.yml", "", nil, "/home/me/q/index.yml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Locate(tt.flag, tt.index, envMap(tt.env), home)
			if got != tt.want {
				t.Errorf("Locate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func loadSample(t *testing.T) *Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "index.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data, "/home/me")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestParseSample(t *testing.T) {
	cfg := loadSample(t)

	if cfg.GlobalContext != "Knowledge base for my projects" {
		t.Errorf("GlobalContext = %q", cfg.GlobalContext)
	}
	if cfg.EditorURI != "vscode://file{path}:{line}:{col}" {
		t.Errorf("EditorURI = %q", cfg.EditorURI)
	}
	if got, want := cfg.Names(), []string{"docs", "journal", "notes"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}

	notes := cfg.Collections["notes"]
	if notes.Name != "notes" {
		t.Errorf("Name = %q", notes.Name)
	}
	if notes.Path != "/home/me/notes" {
		t.Errorf("Path = %q, want tilde expanded", notes.Path)
	}
	if !reflect.DeepEqual(notes.Ignore, []string{"Archive/**", "**/drafts/**"}) {
		t.Errorf("Ignore = %v", notes.Ignore)
	}
	if !notes.Included() {
		t.Error("notes should be included")
	}

	if cfg.Collections["docs"].Included() {
		t.Error("docs has includeByDefault: false")
	}

	journal := cfg.Collections["journal"]
	if journal.Pattern != DefaultPattern {
		t.Errorf("default pattern = %q", journal.Pattern)
	}
	if !journal.Included() {
		t.Error("includeByDefault should default to true")
	}
}

func TestParseRealQMDConfig(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "qmd_index_real.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data, "/home/me")
	if err != nil {
		t.Fatal(err)
	}
	demo := cfg.Collections["demo"]
	if demo == nil {
		t.Fatal("demo collection missing")
	}
	if demo.Path != "/Users/me/lantern-demo" || demo.Pattern != "**/*.md" || !demo.Included() {
		t.Errorf("demo = %+v", demo)
	}
	if got := demo.RootContext(); got != "Demo notes for lantern" {
		t.Errorf("RootContext() = %q", got)
	}
	if got := demo.ContextFor("work/deploy/guide.md"); got != "Work notes: deploys and meetings" {
		t.Errorf("ContextFor(work/...) = %q", got)
	}
}

func TestParseEditorURIAlias(t *testing.T) {
	cfg, err := Parse([]byte("editor_uri_template: \"zed://file{path}\"\n"), "/h")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EditorURI != "zed://file{path}" {
		t.Errorf("EditorURI = %q", cfg.EditorURI)
	}
	if cfg.Collections == nil {
		t.Error("Collections should be non-nil")
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := Parse([]byte("collections: [unclosed"), "/h"); err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yml")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestContextFor(t *testing.T) {
	cfg := loadSample(t)
	notes := cfg.Collections["notes"]
	journal := cfg.Collections["journal"]
	tests := []struct {
		c    *Collection
		rel  string
		want string
	}{
		{notes, "", "Personal notes and ideas"},
		{notes, "ideas/garden.md", "Personal notes and ideas"},
		{notes, "work", "Work-related notes"},
		{notes, "work/meeting.md", "Work-related notes"},
		{notes, "work/deploy/guide.md", "Deploy runbooks"},
		{notes, "workshop/a.md", "Personal notes and ideas"}, // prefix must match whole segments
		{notes, "/work/", "Work-related notes"},
		{journal, "2026/10/01.md", "Entries from 2026"},
		{journal, "2025/01.md", "Daily journal"},
		{cfg.Collections["docs"], "x.md", ""},
	}
	for _, tt := range tests {
		if got := tt.c.ContextFor(tt.rel); got != tt.want {
			t.Errorf("%s.ContextFor(%q) = %q, want %q", tt.c.Name, tt.rel, got, tt.want)
		}
	}
}

func TestPatterns(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string
	}{
		{"**/*.md", []string{"**/*.md"}},
		{"guides/**/*.md,CHANGELOG.md", []string{"guides/**/*.md", "CHANGELOG.md"}},
		{"sources/**/*.md, CO - *.md", []string{"sources/**/*.md", "CO - *.md"}},
		{"**/*.{md,markdown}", []string{"**/*.{md,markdown}"}},
		{"{a,b}/*.md,c.md", []string{"{a,b}/*.md", "c.md"}},
		{" , ", []string{DefaultPattern}},
	}
	for _, tt := range tests {
		c := &Collection{Pattern: tt.pattern}
		if got := c.Patterns(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Patterns(%q) = %q, want %q", tt.pattern, got, tt.want)
		}
	}
}
