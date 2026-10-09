package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/4rji/lantern/internal/library"
	"github.com/4rji/lantern/internal/qmd"
	"github.com/4rji/lantern/internal/qmdconfig"
)

// Tool limits.
const (
	defaultSearchLimit = 8
	maxSearchLimit     = 20
	maxReadLines       = 400
)

// ToolDef is a provider-independent tool definition. Both providers send the
// same name, description and JSON Schema.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Tools are the three tools the model can use, shared by every provider.
var Tools = []ToolDef{
	{
		Name: "search_documents",
		Description: "Search the user's documents with QMD. Returns matching documents with their " +
			"collection, path, title, relevance score and a short snippet. Use mode \"fast\" for " +
			"keyword lookups and \"smart\" for natural-language questions (slower, matches meaning).",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":       map[string]any{"type": "string", "description": "What to search for."},
				"mode":        map[string]any{"type": "string", "enum": []string{"fast", "smart"}, "description": "fast = keywords; smart = keywords plus meaning, reranked. Default fast."},
				"collections": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Collections to search. Default: all collections you may use."},
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchLimit, "description": "Maximum results. Default 8."},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	},
	{
		Name: "read_document",
		Description: fmt.Sprintf("Read a document's text, with line numbers. Returns at most %d lines per "+
			"call; use from_line to continue a long document.", maxReadLines),
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"collection": map[string]any{"type": "string", "description": "Collection name, as returned by search_documents."},
				"path":       map[string]any{"type": "string", "description": "Path inside the collection, as returned by search_documents."},
				"from_line":  map[string]any{"type": "integer", "minimum": 1, "description": "First line to read. Default 1."},
				"max_lines":  map[string]any{"type": "integer", "minimum": 1, "maximum": maxReadLines, "description": fmt.Sprintf("Lines to read. Default and maximum %d.", maxReadLines)},
			},
			"required":             []string{"collection", "path"},
			"additionalProperties": false,
		},
	},
	{
		Name:        "list_collections",
		Description: "List the collections you may use, with their descriptions.",
		Schema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	},
}

// SchemaJSON returns the tool's input schema as JSON.
func (d ToolDef) SchemaJSON() []byte {
	b, _ := json.Marshal(d.Schema)
	return b
}

// Toolbox runs the tools for one question. It only touches the collections the
// user picked, reads files through the library's containment checks, and
// records which documents were used. It is safe for concurrent use.
type Toolbox struct {
	query    func(ctx context.Context, q qmd.QueryRequest) ([]qmd.Result, error)
	cfg      *qmdconfig.Config
	allowed  []string
	progress func(string)

	mu   sync.Mutex
	read []Source // documents read, in order
	hits []Source // search results seen, in order
}

// NewToolbox prepares the tools for a question restricted to allowed collections.
// progress may be nil.
func NewToolbox(query func(context.Context, qmd.QueryRequest) ([]qmd.Result, error), cfg *qmdconfig.Config, allowed []string, progress func(string)) *Toolbox {
	if progress == nil {
		progress = func(string) {}
	}
	return &Toolbox{query: query, cfg: cfg, allowed: allowed, progress: progress}
}

// Allowed returns the collections this toolbox may use.
func (t *Toolbox) Allowed() []string { return slices.Clone(t.allowed) }

// Progress reports a status line to the user.
func (t *Toolbox) Progress(msg string) { t.progress(msg) }

// Call runs a tool by name with its JSON input. A returned error is meant to be
// shown to the model as a failed tool result, not to abort the question.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (string, error) {
	switch name {
	case "search_documents":
		var in struct {
			Query       string   `json:"query"`
			Mode        string   `json:"mode"`
			Collections []string `json:"collections"`
			Limit       int      `json:"limit"`
		}
		if err := decodeInput(input, &in); err != nil {
			return "", err
		}
		return t.search(ctx, in.Query, in.Mode, in.Collections, in.Limit)
	case "read_document":
		var in struct {
			Collection string `json:"collection"`
			Path       string `json:"path"`
			FromLine   int    `json:"from_line"`
			MaxLines   int    `json:"max_lines"`
		}
		if err := decodeInput(input, &in); err != nil {
			return "", err
		}
		return t.readDocument(in.Collection, in.Path, in.FromLine, in.MaxLines)
	case "list_collections":
		return t.listCollections(), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func decodeInput(input json.RawMessage, v any) error {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	if err := json.Unmarshal(input, v); err != nil {
		return fmt.Errorf("invalid tool input: %v", err)
	}
	return nil
}

func (t *Toolbox) search(ctx context.Context, query, mode string, collections []string, limit int) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("query is empty")
	}
	cols := t.allowed
	if len(collections) > 0 {
		cols = nil
		for _, c := range collections {
			if slices.Contains(t.allowed, c) {
				cols = append(cols, c)
			}
		}
		if len(cols) == 0 {
			return "", fmt.Errorf("none of %v may be searched; available collections: %s", collections, strings.Join(t.allowed, ", "))
		}
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)

	req := qmd.FastQuery(query, cols, limit)
	if mode == "smart" {
		req = qmd.SmartQuery(query, cols, limit)
		t.progress(fmt.Sprintf("Searching (smart) for “%s”…", query))
	} else {
		t.progress(fmt.Sprintf("Searching for “%s”…", query))
	}
	results, err := t.query(ctx, req)
	if err != nil {
		return "", fmt.Errorf("search failed: %v", err)
	}
	if len(results) == 0 {
		return fmt.Sprintf("No documents match %q.", query), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d results for %q:\n", len(results), query)
	for i, r := range results {
		col, rel, err := library.ParseURI(r.File)
		if err != nil || !slices.Contains(t.allowed, col) {
			continue
		}
		t.recordHit(Source{Collection: col, Path: rel, Title: r.Title})
		fmt.Fprintf(&b, "\n%d. %s/%s — %q (score %.2f)\n", i+1, col, rel, r.Title, r.Score)
		if s := strings.TrimSpace(r.CleanSnippet()); s != "" {
			for _, line := range strings.Split(s, "\n") {
				b.WriteString("   ")
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
	}
	return b.String(), nil
}

func (t *Toolbox) readDocument(collection, rel string, fromLine, maxLines int) (string, error) {
	if !slices.Contains(t.allowed, collection) {
		return "", fmt.Errorf("collection %q may not be read; available collections: %s", collection, strings.Join(t.allowed, ", "))
	}
	c := t.cfg.Collections[collection]
	if c == nil {
		return "", fmt.Errorf("unknown collection %q", collection)
	}
	rel = strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(rel, collection+"/")), "/")
	if !library.Matches(c, rel) {
		return "", fmt.Errorf("%s/%s is not a document in this collection", collection, rel)
	}
	t.progress(fmt.Sprintf("Reading %s/%s…", collection, rel))
	content, err := library.ReadFile(c, rel)
	if err != nil {
		if errors.Is(err, library.ErrOutsideRoot) {
			return "", errors.New("that path is outside the collection")
		}
		return "", fmt.Errorf("cannot read %s/%s: %v", collection, rel, err)
	}

	title := library.Title(content, rel)
	t.recordRead(Source{Collection: collection, Path: rel, Title: title})

	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if fromLine < 1 {
		fromLine = 1
	}
	if maxLines <= 0 || maxLines > maxReadLines {
		maxLines = maxReadLines
	}
	if fromLine > len(lines) {
		return fmt.Sprintf("%s/%s has only %d lines.", collection, rel, len(lines)), nil
	}
	end := min(fromLine-1+maxLines, len(lines))

	var b strings.Builder
	fmt.Fprintf(&b, "%s/%s — %q\nLines %d-%d of %d:\n", collection, rel, title, fromLine, end, len(lines))
	for i := fromLine - 1; i < end; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i+1, lines[i])
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "[%d more lines; call read_document with from_line=%d to continue]\n", len(lines)-end, end+1)
	}
	return b.String(), nil
}

func (t *Toolbox) listCollections() string {
	t.progress("Listing collections…")
	var b strings.Builder
	if t.cfg.GlobalContext != "" {
		fmt.Fprintf(&b, "About these documents: %s\n\n", t.cfg.GlobalContext)
	}
	b.WriteString("Collections you may use:\n")
	for _, name := range t.allowed {
		desc := ""
		if c := t.cfg.Collections[name]; c != nil {
			desc = c.RootContext()
		}
		if desc != "" {
			fmt.Fprintf(&b, "- %s: %s\n", name, desc)
		} else {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}
	return b.String()
}

func (t *Toolbox) recordRead(s Source) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !containsSource(t.read, s) {
		t.read = append(t.read, s)
	}
}

func (t *Toolbox) recordHit(s Source) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !containsSource(t.hits, s) {
		t.hits = append(t.hits, s)
	}
}

// Sources lists every document the model read, then every search result the
// answer cites as "collection/path" without having read it.
func (t *Toolbox) Sources(answer string) []Source {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := slices.Clone(t.read)
	for _, h := range t.hits {
		if !containsSource(out, h) && strings.Contains(answer, h.Collection+"/"+h.Path) {
			out = append(out, h)
		}
	}
	return out
}

func containsSource(list []Source, s Source) bool {
	return slices.ContainsFunc(list, func(x Source) bool {
		return x.Collection == s.Collection && x.Path == s.Path
	})
}
