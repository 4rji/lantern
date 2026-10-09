package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// demoLibrary writes a collection tree and a QMD config pointing at it.
func demoLibrary(t *testing.T, extraConfig string) (configPath, root string) {
	t.Helper()
	root = t.TempDir()
	files := map[string]string{
		"README.md":                   "# Demo notes\nSee the [deploy guide](work/deploy/guide.md).\n",
		"work/deploy/guide.md":        "---\ntitle: Deploy guide\ntags: [ops, deploy]\n---\n# How to deploy the API\n\n## Build\n\n```go\nfunc main() {}\n```\n\n## Rollback\n\n![diagram](img/diagram.png)\n\nBack to [meeting](../meeting.md).\n",
		"work/deploy/img/diagram.png": "\x89PNG\r\n\x1a\nfake",
		"work/deploy/evil.svg":        `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"work/meeting.md":             "# Weekly meeting\n- [ ] Write the runbook\n",
		"ideas/garden.md":             "Plant tomatoes.\n",
		"Archive/old.md":              "# Old note\n",
		".secret/keys.md":             "# secret\n",
		"My Notes/space name.md":      "# Spaced\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := fmt.Sprintf(`global_context: "My knowledge base"
editor_uri: "zed://file{path}:{line}:{col}"
collections:
  demo:
    path: %s
    ignore: ["Archive/**"]
    context:
      "": "Demo notes for lantern"
      work: "Work notes"
%s`, root, extraConfig)
	configPath = filepath.Join(t.TempDir(), "index.yml")
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return configPath, root
}

func TestLibraryPage(t *testing.T) {
	cfgPath, root := demoLibrary(t, `  away:
    path: /definitely/not/here
    includeByDefault: false
`)
	s := newTestServer(t, &fakeHealth{up: true}, cfgPath)
	resp, body := get(t, s, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	mustContain(t, body,
		"My knowledge base",
		`href="/c/demo/"`, "Demo notes for lantern", "5 documents", root,
		`href="/c/away/"`, "Folder not found on this computer.", "Not in default search",
	)
}

func TestLibraryNoCollections(t *testing.T) {
	p := filepath.Join(t.TempDir(), "index.yml")
	os.WriteFile(p, []byte("global_context: x\n"), 0o644)
	s := newTestServer(t, &fakeHealth{up: true}, p)
	_, body := get(t, s, "/")
	mustContain(t, body, "No collections yet", "qmd collection add")
}

func TestCollectionRoot(t *testing.T) {
	cfgPath, _ := demoLibrary(t, "")
	s := newTestServer(t, &fakeHealth{up: true}, cfgPath)

	resp, _ := get(t, s, "/c/demo")
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/c/demo/" {
		t.Errorf("/c/demo → %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, body := get(t, s, "/c/demo/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	mustContain(t, body,
		"<h1>demo</h1>", "Demo notes for lantern",
		`href="/c/demo/work/"`, `href="/c/demo/ideas/"`, `href="/c/demo/My%20Notes/"`,
		`href="/c/demo/README.md"`, "Demo notes", // README title from its H1
	)
	mustNotContain(t, body, "Archive", ".secret")
}

func TestCollectionFolder(t *testing.T) {
	cfgPath, _ := demoLibrary(t, "")
	s := newTestServer(t, &fakeHealth{up: true}, cfgPath)

	resp, _ := get(t, s, "/c/demo/work")
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/c/demo/work/" {
		t.Errorf("folder without slash → %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	_, body := get(t, s, "/c/demo/work/")
	mustContain(t, body,
		"<h1>work</h1>", "Work notes",
		`<a href="/c/demo/">demo</a>`, // breadcrumb
		`href="/c/demo/work/deploy/"`, `href="/c/demo/work/meeting.md"`, "Weekly meeting",
		`<details open>`, // tree opened on the way to the current folder
	)

	_, body = get(t, s, "/c/demo/My%20Notes/")
	mustContain(t, body, "Spaced", `href="/c/demo/My%20Notes/space%20name.md"`)
}

func TestDocumentPage(t *testing.T) {
	cfgPath, root := demoLibrary(t, "")
	s := newTestServer(t, &fakeHealth{up: true}, cfgPath)
	resp, body := get(t, s, "/c/demo/work/deploy/guide.md")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	abs := filepath.Join(root, "work", "deploy", "guide.md")
	mustContain(t, body,
		"<title>Deploy guide · lantern</title>",
		`<h1 class="doc-heading">Deploy guide</h1>`,
		`<h1 id="how-to-deploy-the-api">How to deploy the API</h1>`, // differs from the title, so kept
		`<span class="chip-key">tags</span><span class="chip-value">ops</span>`,
		"data-raw-toggle",
		`<span class="chip-value">deploy</span>`,
		`href="#build"`, `href="#rollback"`, // TOC
		`<span class="kd">func</span>`,
		`src="/c/demo/work/deploy/img/diagram.png"`,
		`href="/c/demo/work/meeting.md"`,
		`href="zed://file`+abs+`:1:1"`,
		`data-copy="`+abs+`"`,
		"data-raw-toggle", "title: Deploy guide", // raw source present (hidden)
		`<a href="/c/demo/work/">work</a>`,
	)
	mustNotContain(t, body, `<span class="chip-key">title</span>`) // already the page heading
}

func TestDocumentEditorDefaultAndEnv(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("# A\n"), 0o644)
	cfgPath := filepath.Join(t.TempDir(), "index.yml")
	os.WriteFile(cfgPath, []byte("collections:\n  n:\n    path: "+root+"\n"), 0o644)

	s := newTestServer(t, &fakeHealth{up: true}, cfgPath)
	_, body := get(t, s, "/c/n/a.md")
	mustContain(t, body, `href="vscode://file`+filepath.Join(root, "a.md")+`"`)

	s, _ = New(Options{QMDConfigPath: cfgPath, EditorURI: "cursor://file{path}:{line}"}, &fakeHealth{up: true})
	_, body = get(t, s, "/c/n/a.md")
	mustContain(t, body, `href="cursor://file`+filepath.Join(root, "a.md")+`:1"`)
}

func TestEditorURL(t *testing.T) {
	tests := []struct{ tmpl, abs, want string }{
		{"vscode://file{path}", "/Users/me/a.md", "vscode://file/Users/me/a.md"},
		{"vscode://file{path}:{line}:{col}", "/Users/me/My Notes/a.md", "vscode://file/Users/me/My%20Notes/a.md:1:1"},
		{"javascript:alert('{path}')", "/a.md", ""},
		{"no scheme", "/a.md", ""},
	}
	for _, tt := range tests {
		if got := string(editorURL(tt.tmpl, tt.abs)); got != tt.want {
			t.Errorf("editorURL(%q) = %q, want %q", tt.tmpl, got, tt.want)
		}
	}
}

func TestRawFiles(t *testing.T) {
	cfgPath, _ := demoLibrary(t, "")
	s := newTestServer(t, &fakeHealth{up: true}, cfgPath)

	resp, body := get(t, s, "/c/demo/work/deploy/img/diagram.png")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(body, "\x89PNG") {
		t.Errorf("png: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Error("raw files must be sandboxed")
	}
	resp, _ = get(t, s, "/c/demo/work/deploy/evil.svg")
	if resp.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Error("SVG must be sandboxed")
	}
}

func TestCollectionNotFoundAndTraversal(t *testing.T) {
	cfgPath, _ := demoLibrary(t, "")
	outside := filepath.Join(filepath.Dir(cfgPath), "secret.md")
	os.WriteFile(outside, []byte("# outside\n"), 0o644)

	s := newTestServer(t, &fakeHealth{up: true}, cfgPath)
	for _, target := range []string{
		"/c/nope/",
		"/c/demo/missing.md",
		"/c/demo/.secret/keys.md",
		"/c/demo/.secret/",
		"/c/demo/%2e%2e/%2e%2e/etc/passwd",
		"/c/demo/..%2f..%2fsecret.md",
		"/c/demo/work/%2e%2e%2f%2e%2e%2f%2e%2e%2fetc%2fpasswd",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Host = "127.0.0.1"
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		code := rec.Code
		if code == http.StatusOK && strings.Contains(rec.Body.String(), "outside") {
			t.Errorf("%s leaked a file outside the collection", target)
		}
		if code != http.StatusNotFound && code != http.StatusMovedPermanently && code != http.StatusTemporaryRedirect {
			t.Errorf("%s → %d, want 404 or redirect", target, code)
		}
	}
}

func TestCollectionMissingFolder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "index.yml")
	os.WriteFile(p, []byte("collections:\n  mac:\n    path: /Users/someone/notes\n"), 0o644)
	s := newTestServer(t, &fakeHealth{up: true}, p)
	resp, body := get(t, s, "/c/mac/")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	mustContain(t, body, "Folder not found on this computer.", "/Users/someone/notes")
}

func TestCrumbs(t *testing.T) {
	got := crumbs("demo", "work/deploy", "Guide")
	want := []crumb{
		{"Library", "/"}, {"demo", "/c/demo/"}, {"work", "/c/demo/work/"}, {"deploy", "/c/demo/work/deploy/"}, {"Guide", ""},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("crumbs = %v", got)
	}
	if got := crumbs("demo", ".", ""); fmt.Sprint(got) != fmt.Sprint([]crumb{{"Library", "/"}, {"demo", ""}}) {
		t.Errorf("root crumbs = %v", got)
	}
	if got := crumbs("demo", "work", ""); fmt.Sprint(got) != fmt.Sprint([]crumb{{"Library", "/"}, {"demo", "/c/demo/"}, {"work", ""}}) {
		t.Errorf("folder crumbs = %v", got)
	}
}
