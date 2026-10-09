package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/4rji/lantern/internal/qmd"
	"github.com/4rji/lantern/internal/qmdconfig"
)

// testLibrary writes two collections and returns their config.
func testLibrary(t *testing.T) *qmdconfig.Config {
	t.Helper()
	demo, other := t.TempDir(), t.TempDir()
	write := func(root, rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var long strings.Builder
	for i := 1; i <= 450; i++ {
		fmt.Fprintf(&long, "line %d\n", i)
	}
	write(demo, "work/deploy/guide.md", "---\ntitle: Deploy guide\n---\n# How to deploy\n\nRoll back with `kubectl rollout undo`.\n")
	write(demo, "long.md", long.String())
	write(demo, ".env.md", "SECRET=1\n")
	write(demo, "notes.txt", "not markdown\n")
	write(other, "private.md", "# Private\n")
	os.WriteFile(filepath.Join(filepath.Dir(demo), "outside.md"), []byte("outside"), 0o644)

	cfg, err := qmdconfig.Parse([]byte(fmt.Sprintf(`global_context: "My notes"
collections:
  demo:
    path: %s
    context:
      "": Demo notes
  other:
    path: %s
`, demo, other)), "/home/me")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

type queryRecorder struct {
	mu   sync.Mutex
	reqs []qmd.QueryRequest
}

func (q *queryRecorder) query(ctx context.Context, r qmd.QueryRequest) ([]qmd.Result, error) {
	q.mu.Lock()
	q.reqs = append(q.reqs, r)
	q.mu.Unlock()
	return []qmd.Result{
		{File: "qmd://demo/work/deploy/guide.md", Title: "How to deploy", Score: 0.92, Snippet: "4: @@ -4,2 @@ (0 before, 1 after)\n5: # How to deploy\n6: Roll back"},
		{File: "qmd://other/private.md", Title: "Private", Score: 0.5},
	}, nil
}

func newTestToolbox(t *testing.T) (*Toolbox, *queryRecorder, *[]string) {
	q := &queryRecorder{}
	var progress []string
	var mu sync.Mutex
	tb := NewToolbox(q.query, testLibrary(t), []string{"demo"}, func(s string) {
		mu.Lock()
		progress = append(progress, s)
		mu.Unlock()
	})
	return tb, q, &progress
}

func call(t *testing.T, tb *Toolbox, name, input string) (string, error) {
	t.Helper()
	return tb.Call(context.Background(), name, json.RawMessage(input))
}

func TestToolSchemas(t *testing.T) {
	names := []string{}
	for _, d := range Tools {
		names = append(names, d.Name)
		var schema map[string]any
		if err := json.Unmarshal(d.SchemaJSON(), &schema); err != nil || schema["type"] != "object" {
			t.Errorf("%s: bad schema %s", d.Name, d.SchemaJSON())
		}
	}
	if !reflect.DeepEqual(names, []string{"search_documents", "read_document", "list_collections"}) {
		t.Errorf("tools = %v", names)
	}
}

func TestSearchDocuments(t *testing.T) {
	tb, q, progress := newTestToolbox(t)
	out, err := call(t, tb, "search_documents", `{"query":"deploy"}`)
	if err != nil {
		t.Fatal(err)
	}
	got := q.reqs[0]
	if got.Searches[0].Query != "deploy" || got.Rerank || !reflect.DeepEqual(got.Collections, []string{"demo"}) || got.Limit != defaultSearchLimit {
		t.Errorf("fast request = %+v", got)
	}
	if !strings.Contains(out, `demo/work/deploy/guide.md — "How to deploy" (score 0.92)`) || !strings.Contains(out, "   Roll back") {
		t.Errorf("output:\n%s", out)
	}
	if strings.Contains(out, "private.md") || strings.Contains(out, "@@") {
		t.Errorf("output leaks a non-allowed collection or raw snippet:\n%s", out)
	}
	if (*progress)[0] != "Searching for “deploy”…" {
		t.Errorf("progress = %v", *progress)
	}

	call(t, tb, "search_documents", `{"query":"how to ship","mode":"smart","limit":500,"collections":["demo","other"]}`)
	got = q.reqs[1]
	if !got.Rerank || len(got.Searches) != 2 || got.Limit != maxSearchLimit || !reflect.DeepEqual(got.Collections, []string{"demo"}) {
		t.Errorf("smart request = %+v", got)
	}

	if _, err := call(t, tb, "search_documents", `{"query":"x","collections":["other"]}`); err == nil || !strings.Contains(err.Error(), "available collections: demo") {
		t.Errorf("forbidden collection: err = %v", err)
	}
	if _, err := call(t, tb, "search_documents", `{"query":"  "}`); err == nil {
		t.Error("empty query should fail")
	}
	if _, err := call(t, tb, "search_documents", `{"query":`); err == nil {
		t.Error("invalid JSON should fail")
	}
}

func TestReadDocument(t *testing.T) {
	tb, _, progress := newTestToolbox(t)
	out, err := call(t, tb, "read_document", `{"collection":"demo","path":"work/deploy/guide.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`demo/work/deploy/guide.md — "Deploy guide"`, "Lines 1-6 of 6:", "6: Roll back with `kubectl rollout undo`."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if (*progress)[0] != "Reading demo/work/deploy/guide.md…" {
		t.Errorf("progress = %v", *progress)
	}

	// The model sometimes repeats the collection name in the path.
	if _, err := call(t, tb, "read_document", `{"collection":"demo","path":"demo/work/deploy/guide.md"}`); err != nil {
		t.Errorf("collection-prefixed path: %v", err)
	}

	out, _ = call(t, tb, "read_document", `{"collection":"demo","path":"long.md"}`)
	if !strings.Contains(out, "Lines 1-400 of 450") || !strings.Contains(out, "from_line=401") || strings.Contains(out, "401: line 401") {
		t.Errorf("long doc first page wrong:\n%.200s", out)
	}
	out, _ = call(t, tb, "read_document", `{"collection":"demo","path":"long.md","from_line":440,"max_lines":5}`)
	if !strings.Contains(out, "Lines 440-444 of 450") || !strings.Contains(out, "444: line 444") {
		t.Errorf("window wrong:\n%s", out)
	}
	out, _ = call(t, tb, "read_document", `{"collection":"demo","path":"long.md","from_line":999}`)
	if !strings.Contains(out, "has only 450 lines") {
		t.Errorf("past the end: %s", out)
	}

	for _, input := range []string{
		`{"collection":"other","path":"private.md"}`,
		`{"collection":"demo","path":"../outside.md"}`,
		`{"collection":"demo","path":"../../../etc/passwd"}`,
		`{"collection":"demo","path":".env.md"}`,
		`{"collection":"demo","path":"notes.txt"}`,
		`{"collection":"demo","path":"missing.md"}`,
	} {
		if out, err := call(t, tb, "read_document", input); err == nil {
			t.Errorf("%s should fail, got:\n%s", input, out)
		}
	}
}

func TestListCollectionsAndUnknownTool(t *testing.T) {
	tb, _, _ := newTestToolbox(t)
	out, err := call(t, tb, "list_collections", ``)
	if err != nil || !strings.Contains(out, "About these documents: My notes") || !strings.Contains(out, "- demo: Demo notes") || strings.Contains(out, "other") {
		t.Errorf("list_collections = %q, %v", out, err)
	}
	if _, err := call(t, tb, "delete_everything", `{}`); err == nil {
		t.Error("unknown tool should fail")
	}
}

func TestSources(t *testing.T) {
	tb, _, _ := newTestToolbox(t)
	call(t, tb, "search_documents", `{"query":"deploy"}`)
	if got := tb.Sources("I could not find anything."); len(got) != 0 {
		t.Errorf("uncited search hits must not be sources: %v", got)
	}
	got := tb.Sources("See `demo/work/deploy/guide.md`.")
	if len(got) != 1 || got[0] != (Source{Collection: "demo", Path: "work/deploy/guide.md", Title: "How to deploy"}) {
		t.Errorf("cited hit: %v", got)
	}
	call(t, tb, "read_document", `{"collection":"demo","path":"long.md"}`)
	call(t, tb, "read_document", `{"collection":"demo","path":"long.md","from_line":401}`)
	got = tb.Sources("")
	if len(got) != 1 || got[0].Path != "long.md" {
		t.Errorf("read documents (deduplicated): %v", got)
	}
}
