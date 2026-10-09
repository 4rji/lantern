package library

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/4rji/lantern/internal/qmdconfig"
)

// makeTree writes files (slash paths → content) under a new temp dir.
func makeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

var demoFiles = map[string]string{
	"README.md":                  "# Demo notes\nSee [guide](work/deploy/guide.md).\n",
	"work/deploy/guide.md":       "---\ntitle: Deploy guide\ntags: [ops]\n---\n# How to deploy the API\n",
	"work/meeting-2026-10-01.md": "# Weekly meeting\n- [ ] item\n",
	"ideas/garden.md":            "Plant tomatoes.\n",
	"ideas/drafts/wip.md":        "# WIP\n",
	"Archive/old.md":             "# Old note\n",
	"node_modules/pkg/readme.md": "# dependency\n",
	".git/info.md":               "# git\n",
	".obsidian/notes.md":         "# hidden folder\n",
	"ideas/.draft.md":            "# hidden file\n",
	"images/diagram.png":         "png",
	"notes.txt":                  "text",
}

func paths(docs []Doc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.Path
	}
	return out
}

func TestWalk(t *testing.T) {
	root := makeTree(t, demoFiles)
	tests := []struct {
		name string
		c    qmdconfig.Collection
		want []string
	}{
		{
			name: "default pattern, built-in excludes",
			c:    qmdconfig.Collection{Pattern: "**/*.md"},
			want: []string{"Archive/old.md", "README.md", "ideas/drafts/wip.md", "ideas/garden.md", "work/deploy/guide.md", "work/meeting-2026-10-01.md"},
		},
		{
			name: "ignore rules",
			c:    qmdconfig.Collection{Pattern: "**/*.md", Ignore: []string{"Archive/**", "**/drafts/**"}},
			want: []string{"README.md", "ideas/garden.md", "work/deploy/guide.md", "work/meeting-2026-10-01.md"},
		},
		{
			name: "ignore a single file",
			c:    qmdconfig.Collection{Pattern: "**/*.md", Ignore: []string{"README.md", "**/meeting-*.md"}},
			want: []string{"Archive/old.md", "ideas/drafts/wip.md", "ideas/garden.md", "work/deploy/guide.md"},
		},
		{
			name: "comma-separated union pattern",
			c:    qmdconfig.Collection{Pattern: "work/**/*.md,README.md"},
			want: []string{"README.md", "work/deploy/guide.md", "work/meeting-2026-10-01.md"},
		},
		{
			name: "brace pattern",
			c:    qmdconfig.Collection{Pattern: "{ideas,Archive}/*.{md,txt}"},
			want: []string{"Archive/old.md", "ideas/garden.md"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.c
			c.Name, c.Path = "demo", root
			docs, err := Walk(&c)
			if err != nil {
				t.Fatal(err)
			}
			if got := paths(docs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Walk() = %v\nwant      %v", got, tt.want)
			}
			for _, d := range docs {
				if d.Collection != "demo" {
					t.Errorf("%s: Collection = %q", d.Path, d.Collection)
				}
			}
		})
	}
}

func TestWalkDocFields(t *testing.T) {
	root := makeTree(t, demoFiles)
	mtime := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(root, "work", "deploy", "guide.md"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	docs, err := Walk(&qmdconfig.Collection{Name: "demo", Path: root, Pattern: "**/*.md"})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Doc{}
	for _, d := range docs {
		byPath[d.Path] = d
	}

	guide := byPath["work/deploy/guide.md"]
	if guide.Title != "Deploy guide" {
		t.Errorf("frontmatter title: got %q", guide.Title)
	}
	if !guide.ModTime.Equal(mtime) {
		t.Errorf("ModTime = %v, want %v", guide.ModTime, mtime)
	}
	if guide.Size != int64(len(demoFiles["work/deploy/guide.md"])) {
		t.Errorf("Size = %d", guide.Size)
	}
	if got := byPath["README.md"].Title; got != "Demo notes" {
		t.Errorf("H1 title: got %q", got)
	}
	if got := byPath["ideas/garden.md"].Title; got != "garden" {
		t.Errorf("fallback title: got %q", got)
	}
}

func TestWalkMissingRoot(t *testing.T) {
	_, err := Walk(&qmdconfig.Collection{Name: "x", Path: filepath.Join(t.TempDir(), "missing"), Pattern: "**/*.md"})
	if err == nil {
		t.Error("expected error for missing collection folder")
	}
}

func TestMatches(t *testing.T) {
	c := &qmdconfig.Collection{Pattern: "**/*.md", Ignore: []string{"Archive/**"}}
	tests := map[string]bool{
		"README.md":          true,
		"a/b/c.md":           true,
		"Archive/old.md":     false,
		"node_modules/x.md":  false,
		"a/dist/x.md":        false,
		"images/diagram.png": false,
		".hidden/x.md":       false,
		"a/.x.md":            false,
	}
	for rel, want := range tests {
		if got := Matches(c, rel); got != want {
			t.Errorf("Matches(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestTitle(t *testing.T) {
	tests := []struct {
		name, content, rel, want string
	}{
		{"frontmatter", "---\ntitle: From FM\n---\n# H1\n", "a.md", "From FM"},
		{"frontmatter crlf", "---\r\ntitle: CRLF\r\n---\r\n# H1\r\n", "a.md", "CRLF"},
		{"frontmatter without title", "---\ntags: [x]\n---\n# The H1\n", "a.md", "The H1"},
		{"numeric title", "---\ntitle: 2026\n---\n", "a.md", "2026"},
		{"h1 closing hashes", "# Closed #\n", "a.md", "Closed"},
		{"h2 is not a title", "## Section\ntext\n", "dir/note.md", "note"},
		{"h1 in code fence skipped", "```\n# not a title\n```\n# Real\n", "a.md", "Real"},
		{"setext h1", "Setext Title\n============\n", "a.md", "Setext Title"},
		{"hashtag is not heading", "#tag line\n", "x/y.md", "y"},
		{"unterminated frontmatter", "---\ntitle: nope\n# Heading\n", "a.md", "Heading"},
		{"empty", "", "folder/My Note.md", "My Note"},
		{"bom", "\xef\xbb\xbf---\ntitle: BOM\n---\n", "a.md", "BOM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Title([]byte(tt.content), tt.rel); got != tt.want {
				t.Errorf("Title() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplitFrontmatter(t *testing.T) {
	fm, body := SplitFrontmatter([]byte("---\na: 1\nb: 2\n---\nbody\n"))
	if string(fm) != "a: 1\nb: 2\n" || string(body) != "body\n" {
		t.Errorf("fm=%q body=%q", fm, body)
	}
	fm, body = SplitFrontmatter([]byte("no frontmatter\n---\n"))
	if fm != nil || string(body) != "no frontmatter\n---\n" {
		t.Errorf("fm=%q body=%q", fm, body)
	}
	fm, body = SplitFrontmatter([]byte("---\na: 1\n---"))
	if string(fm) != "a: 1\n" || len(body) != 0 {
		t.Errorf("no trailing newline: fm=%q body=%q", fm, body)
	}
}

func TestBuildTree(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	docs := []Doc{
		{Path: "README.md", ModTime: t1},
		{Path: "work/deploy/guide.md", ModTime: t2},
		{Path: "work/meeting.md", ModTime: t1},
		{Path: "ideas/garden.md", ModTime: t1},
	}
	root := BuildTree(docs)
	if root.DocCount != 4 || !root.ModTime.Equal(t2) {
		t.Errorf("root DocCount=%d ModTime=%v", root.DocCount, root.ModTime)
	}
	var names []string
	for _, f := range root.Folders {
		names = append(names, f.Name)
	}
	if !reflect.DeepEqual(names, []string{"ideas", "work"}) {
		t.Errorf("root folders = %v", names)
	}
	if len(root.Docs) != 1 || root.Docs[0].Path != "README.md" {
		t.Errorf("root docs = %v", root.Docs)
	}

	work := root.Find("work")
	if work == nil || work.DocCount != 2 || work.Path != "work" {
		t.Fatalf("work = %+v", work)
	}
	deploy := root.Find("/work/deploy/")
	if deploy == nil || deploy.Path != "work/deploy" || len(deploy.Docs) != 1 {
		t.Fatalf("deploy = %+v", deploy)
	}
	if root.Find("") != root {
		t.Error("Find(\"\") should return root")
	}
	if root.Find("nope") != nil || root.Find("work/nope") != nil {
		t.Error("Find of missing folder should be nil")
	}
}

func TestParseURI(t *testing.T) {
	tests := []struct {
		uri, coll, rel string
		ok             bool
	}{
		{"qmd://demo/work/deploy/guide.md", "demo", "work/deploy/guide.md", true},
		{"qmd://demo/README.md", "demo", "README.md", true},
		{"qmd://demo/", "", "", false},
		{"qmd://demo", "", "", false},
		{"qmd:///x.md", "", "", false},
		{"file:///etc/passwd", "", "", false},
	}
	for _, tt := range tests {
		c, r, err := ParseURI(tt.uri)
		if (err == nil) != tt.ok || c != tt.coll || r != tt.rel {
			t.Errorf("ParseURI(%q) = %q, %q, %v", tt.uri, c, r, err)
		}
	}
}

func TestResolve(t *testing.T) {
	root := makeTree(t, demoFiles)
	outside := makeTree(t, map[string]string{"secret.md": "secret"})
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	cfg := &qmdconfig.Config{Collections: map[string]*qmdconfig.Collection{
		"demo": {Name: "demo", Path: root, Pattern: "**/*.md"},
	}}

	tests := []struct {
		uri     string
		wantRel string
		wantErr error
	}{
		{"qmd://demo/work/deploy/guide.md", "work/deploy/guide.md", nil},
		{"qmd://demo/work/../README.md", "README.md", nil},
		{"qmd://demo/../../etc/passwd", "", ErrOutsideRoot},
		{"qmd://demo/work/../../outside.md", "", ErrOutsideRoot},
		{"qmd://demo/link.md", "", ErrOutsideRoot}, // symlink escaping the root
		{"qmd://other/README.md", "", ErrUnknownCollection},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			c, rel, abs, err := Resolve(cfg, tt.uri)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Name != "demo" || rel != tt.wantRel || abs != filepath.Join(root, filepath.FromSlash(tt.wantRel)) {
				t.Errorf("got %s %q %q", c.Name, rel, abs)
			}
		})
	}
}

func TestReadFile(t *testing.T) {
	root := makeTree(t, demoFiles)
	c := &qmdconfig.Collection{Name: "demo", Path: root}

	got, err := ReadFile(c, "work/meeting-2026-10-01.md")
	if err != nil || string(got) != demoFiles["work/meeting-2026-10-01.md"] {
		t.Errorf("ReadFile = %q, %v", got, err)
	}
	if _, err := ReadFile(c, "../../../etc/passwd"); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("traversal: err = %v, want ErrOutsideRoot", err)
	}
	if _, err := ReadFile(c, "work"); err == nil {
		t.Error("reading a folder should fail")
	}
	if _, err := ReadFile(c, "missing.md"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: err = %v", err)
	}
}
