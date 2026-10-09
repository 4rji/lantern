// Package render converts markdown to HTML (goldmark), with frontmatter and a TOC.
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"path"
	"strings"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v2"

	"github.com/4rji/lantern/internal/library"
)

// Chroma styles used for code blocks in each theme.
const (
	darkCodeStyle  = "github-dark"
	lightCodeStyle = "github"
)

// Heading is one table-of-contents entry.
type Heading struct {
	Level int
	ID    string
	Text  string
}

// MetaItem is one frontmatter key with its value(s), in file order.
type MetaItem struct {
	Key    string
	Values []string
}

// Document is a rendered markdown file.
type Document struct {
	HTML template.HTML
	TOC  []Heading
	Meta []MetaItem
}

// Location tells the renderer where a document lives, so relative links and
// images can point at dashboard URLs.
type Location struct {
	Collection string
	Path       string // slash-separated, relative to the collection root
	// Title is the page title shown above the document. A leading H1 with the
	// same text is dropped so the title does not appear twice.
	Title string
}

// Renderer converts markdown to HTML. It is safe for concurrent use.
type Renderer struct {
	md goldmark.Markdown
}

// New returns a Renderer with GFM, footnotes, frontmatter, heading IDs and
// class-based syntax highlighting. Raw HTML in markdown is not rendered.
func New() *Renderer {
	return &Renderer{md: goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Footnote,
			meta.Meta,
			highlighting.NewHighlighting(
				highlighting.WithGuessLanguage(false),
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
			),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)}
}

// Render converts source to HTML, rewriting relative links for loc.
func (r *Renderer) Render(source []byte, loc Location) (Document, error) {
	pc := parser.NewContext()
	root := r.md.Parser().Parse(text.NewReader(source), parser.WithContext(pc))

	if first, ok := root.FirstChild().(*ast.Heading); ok && first.Level == 1 &&
		loc.Title != "" && nodeText(first, source) == loc.Title {
		root.RemoveChild(root, first)
	}

	var toc []Heading
	err := ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Link:
			n.Destination = []byte(ResolveLink(loc, string(n.Destination)))
			if isExternal(string(n.Destination)) {
				n.SetAttributeString("target", "_blank")
				n.SetAttributeString("rel", "noopener noreferrer")
			}
		case *ast.AutoLink:
			if n.AutoLinkType == ast.AutoLinkURL && isExternal(string(n.URL(source))) {
				n.SetAttributeString("target", "_blank")
				n.SetAttributeString("rel", "noopener noreferrer")
			}
		case *ast.Image:
			n.Destination = []byte(ResolveLink(loc, string(n.Destination)))
			n.SetAttributeString("loading", "lazy")
		case *ast.Heading:
			if n.Level <= 4 {
				id, _ := n.AttributeString("id")
				idBytes, _ := id.([]byte)
				toc = append(toc, Heading{Level: n.Level, ID: string(idBytes), Text: nodeText(n, source)})
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return Document{}, err
	}

	var buf bytes.Buffer
	if err := r.md.Renderer().Render(&buf, source, root); err != nil {
		return Document{}, err
	}
	return Document{
		HTML: template.HTML(buf.String()), // goldmark escapes text and drops raw HTML
		TOC:  trimTOC(toc),
		Meta: metaItems(pc),
	}, nil
}

// ResolveLink maps a link destination found in the document at loc to a
// dashboard URL. Absolute URLs, anchors, links that leave the collection, and
// any link when loc has no collection (e.g. an AI answer) are returned unchanged.
func ResolveLink(loc Location, dest string) string {
	if loc.Collection == "" || dest == "" || strings.HasPrefix(dest, "#") {
		return dest
	}
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
		return dest
	}
	var rel string
	if strings.HasPrefix(u.Path, "/") {
		rel = path.Clean(strings.TrimPrefix(u.Path, "/"))
	} else {
		rel = path.Join(path.Dir(loc.Path), u.Path)
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return dest
	}
	if rel == "." {
		rel = ""
	}
	folder := strings.HasSuffix(u.Path, "/") || rel == ""
	out := library.WebPath(loc.Collection, rel, folder)
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + url.PathEscape(u.Fragment)
	}
	return out
}

func isExternal(dest string) bool {
	u, err := url.Parse(dest)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}

// nodeText concatenates the text inside an inline container (e.g. a heading).
func nodeText(n ast.Node, source []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch t := c.(type) {
			case *ast.Text:
				b.Write(t.Segment.Value(source))
				if t.SoftLineBreak() || t.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.Write(t.Value)
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

// trimTOC drops a lone H1 (it is the page title) and returns nil when fewer
// than two entries remain.
func trimTOC(toc []Heading) []Heading {
	h1 := 0
	for _, h := range toc {
		if h.Level == 1 {
			h1++
		}
	}
	if h1 == 1 {
		out := toc[:0:0]
		for _, h := range toc {
			if h.Level != 1 {
				out = append(out, h)
			}
		}
		toc = out
	}
	if len(toc) < 2 {
		return nil
	}
	return toc
}

func metaItems(pc parser.Context) []MetaItem {
	items, err := meta.TryGetItems(pc)
	if err != nil || len(items) == 0 {
		return nil
	}
	out := make([]MetaItem, 0, len(items))
	for _, it := range items {
		key := fmt.Sprint(it.Key)
		var values []string
		switch v := it.Value.(type) {
		case nil:
			continue
		case []any:
			for _, e := range v {
				values = append(values, scalar(e))
			}
		default:
			values = []string{scalar(v)}
		}
		out = append(out, MetaItem{Key: key, Values: values})
	}
	return out
}

func scalar(v any) string {
	switch t := v.(type) {
	case time.Time:
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
			return t.Format("2006-01-02")
		}
		return t.Format("2006-01-02 15:04")
	case yaml.MapSlice: // nested mapping, in file order
		parts := make([]string, 0, len(t))
		for _, it := range t {
			parts = append(parts, fmt.Sprint(it.Key)+": "+scalar(it.Value))
		}
		return strings.Join(parts, ", ")
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, scalar(e))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(v)
	}
}

// CodeCSS returns the syntax-highlighting stylesheet for both themes, scoped
// under [data-theme="dark"] and [data-theme="light"].
func CodeCSS() (string, error) {
	var out strings.Builder
	for _, t := range []struct{ theme, style string }{{"dark", darkCodeStyle}, {"light", lightCodeStyle}} {
		var buf bytes.Buffer
		f := chromahtml.New(chromahtml.WithClasses(true))
		if err := f.WriteCSS(&buf, styles.Get(t.style)); err != nil {
			return "", err
		}
		scope := `[data-theme="` + t.theme + `"] `
		for _, line := range strings.Split(buf.String(), "\n") {
			// Lines look like: /* Keyword */ .chroma .k { color: #ff7b72 }
			if i := strings.Index(line, "*/ "); i >= 0 {
				line = line[:i+3] + scope + line[i+3:]
			}
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String(), nil
}
