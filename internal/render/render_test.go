package render

import (
	"reflect"
	"strings"
	"testing"
)

var loc = Location{Collection: "demo", Path: "work/deploy/guide.md"}

func renderString(t *testing.T, src string) Document {
	t.Helper()
	doc, err := New().Render([]byte(src), loc)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func contains(t *testing.T, html string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(html, s) {
			t.Errorf("HTML does not contain %q\n%s", s, html)
		}
	}
}

func TestRenderGFM(t *testing.T) {
	src := "# Title\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n- [ ] todo\n- [x] done\n\n~~gone~~ https://example.com\n"
	html := string(renderString(t, src).HTML)
	contains(t, html,
		`<h1 id="title">Title</h1>`,
		"<table>", "<td>1</td>",
		`<input disabled="" type="checkbox"`, `checked=""`,
		"<del>gone</del>",
		`<a href="https://example.com" target="_blank" rel="noopener noreferrer">`,
	)
}

func TestRenderCodeHighlighting(t *testing.T) {
	html := string(renderString(t, "```go\nfunc main() {}\n```\n\n```\nplain\n```\n").HTML)
	contains(t, html, `class="chroma"`, `<span class="kd">func</span>`, "plain")
	if strings.Contains(html, "style=") {
		t.Error("code should use classes, not inline styles (needed for two themes)")
	}
}

func TestRenderRawHTMLOmitted(t *testing.T) {
	html := string(renderString(t, "hi <script>alert(1)</script>\n\n<div onclick=x>block</div>\n").HTML)
	if strings.Contains(html, "<script>") || strings.Contains(html, "onclick") {
		t.Errorf("raw HTML must not be rendered:\n%s", html)
	}
}

func TestRenderFrontmatter(t *testing.T) {
	src := "---\ntitle: Deploy guide\ntags: [ops, deploy]\ndate: 2026-10-01\ndraft: false\nempty:\nauthor: {name: Me, team: Ops}\n---\n# How to deploy\n\nBody text.\n"
	doc := renderString(t, src)
	want := []MetaItem{
		{"title", []string{"Deploy guide"}},
		{"tags", []string{"ops", "deploy"}},
		{"date", []string{"2026-10-01"}},
		{"draft", []string{"false"}},
		{"author", []string{"name: Me, team: Ops"}},
	}
	if !reflect.DeepEqual(doc.Meta, want) {
		t.Errorf("Meta =\n%v\nwant\n%v", doc.Meta, want)
	}
	html := string(doc.HTML)
	if strings.Contains(html, "title: Deploy guide") {
		t.Error("frontmatter leaked into the body")
	}
	contains(t, html, "Body text.")
}

func TestRenderNoFrontmatter(t *testing.T) {
	if doc := renderString(t, "# Just text\n"); doc.Meta != nil {
		t.Errorf("Meta = %v, want nil", doc.Meta)
	}
}

func TestTOC(t *testing.T) {
	src := "# Page title\n\n## Install `go`\n\ntext\n\n### Step *one*\n\n## Rollback\n\n##### too deep\n"
	got := renderString(t, src).TOC
	want := []Heading{
		{2, "install-go", "Install go"},
		{3, "step-one", "Step one"},
		{2, "rollback", "Rollback"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TOC =\n%v\nwant\n%v", got, want)
	}

	if toc := renderString(t, "# Only\n\n## One section\n").TOC; toc != nil {
		t.Errorf("a single section should give no TOC, got %v", toc)
	}
	multiH1 := renderString(t, "# A\n\n# B\n").TOC
	if len(multiH1) != 2 {
		t.Errorf("several H1s should stay in the TOC, got %v", multiH1)
	}
}

func TestResolveLink(t *testing.T) {
	tests := []struct {
		dest, want string
	}{
		{"other.md", "/c/demo/work/deploy/other.md"},
		{"../meeting.md", "/c/demo/work/meeting.md"},
		{"../../README.md#setup", "/c/demo/README.md#setup"},
		{"./img/diagram.png", "/c/demo/work/deploy/img/diagram.png"},
		{"/ideas/garden.md", "/c/demo/ideas/garden.md"},
		{"../", "/c/demo/work/"},
		{"../../", "/c/demo/"},
		{"My%20Note.md", "/c/demo/work/deploy/My%20Note.md"},
		{"notes/a b.md", "/c/demo/work/deploy/notes/a%20b.md"},
		{"../../../outside.md", "../../../outside.md"},
		{"#section", "#section"},
		{"https://example.com/x.md", "https://example.com/x.md"},
		{"//cdn.example.com/x.png", "//cdn.example.com/x.png"},
		{"mailto:me@example.com", "mailto:me@example.com"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := ResolveLink(loc, tt.dest); got != tt.want {
			t.Errorf("ResolveLink(%q) = %q, want %q", tt.dest, got, tt.want)
		}
	}
}

func TestRenderRewritesLinksAndImages(t *testing.T) {
	html := string(renderString(t, "[runbook](../runbook.md) ![diagram](diagram.png)\n").HTML)
	contains(t, html,
		`<a href="/c/demo/work/runbook.md">runbook</a>`,
		`<img src="/c/demo/work/deploy/diagram.png" alt="diagram" loading="lazy">`,
	)
}

func TestCodeCSS(t *testing.T) {
	css, err := CodeCSS()
	if err != nil {
		t.Fatal(err)
	}
	contains(t, css, `[data-theme="dark"] .chroma .k {`, `[data-theme="light"] .chroma .k {`)
	for _, line := range strings.Split(css, "\n") {
		if strings.Contains(line, "{") && !strings.Contains(line, "[data-theme=") {
			t.Errorf("unscoped rule: %s", line)
		}
	}
}

func TestRenderDropsDuplicateTitle(t *testing.T) {
	r := New()
	doc, err := r.Render([]byte("# How to deploy\n\ntext\n"), Location{Collection: "demo", Path: "a.md", Title: "How to deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc.HTML), "<h1") {
		t.Errorf("duplicate H1 kept:\n%s", doc.HTML)
	}
	doc, _ = r.Render([]byte("# How to deploy the API\n\ntext\n"), Location{Collection: "demo", Path: "a.md", Title: "Deploy guide"})
	if !strings.Contains(string(doc.HTML), `<h1 id="how-to-deploy-the-api">`) {
		t.Errorf("H1 that differs from the title must stay:\n%s", doc.HTML)
	}
}
