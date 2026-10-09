package server

import (
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/4rji/lantern/internal/library"
	"github.com/4rji/lantern/internal/qmdconfig"
	"github.com/4rji/lantern/internal/render"
)

// defaultEditorURI matches QMD's default when neither QMD_EDITOR_URI nor
// editor_uri is set.
const defaultEditorURI = "vscode://file{path}"

// crumb is one breadcrumb link. The last crumb has no URL.
type crumb struct {
	Name string
	URL  string
}

// ---------- Library ----------

type collectionCard struct {
	Name     string
	URL      string
	Context  string
	Path     string
	Included bool
	DocCount int
	ModTime  time.Time
	Err      string
}

type libraryData struct {
	GlobalContext string
	Cards         []collectionCard
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	p := s.newPage(r, "Library", "library", nil)
	data := libraryData{}
	if p.Config != nil {
		data.GlobalContext = p.Config.GlobalContext
		for _, c := range p.Config.Sorted() {
			card := collectionCard{
				Name:     c.Name,
				URL:      library.WebPath(c.Name, "", true),
				Context:  c.RootContext(),
				Path:     c.Path,
				Included: c.Included(),
			}
			docs, err := s.docs.Docs(c)
			if err != nil {
				card.Err = friendlyWalkError(err)
			}
			card.DocCount = len(docs)
			for _, d := range docs {
				if d.ModTime.After(card.ModTime) {
					card.ModTime = d.ModTime
				}
			}
			data.Cards = append(data.Cards, card)
		}
	}
	p.Data = data
	s.render(w, r, http.StatusOK, "library", p)
}

func friendlyWalkError(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "Folder not found on this computer."
	case errors.Is(err, fs.ErrPermission):
		return "No permission to read this folder."
	default:
		return err.Error()
	}
}

// ---------- Collection folder + document ----------

type treeNode struct {
	Name     string
	URL      string
	Count    int
	Open     bool
	Current  bool
	Children []treeNode
}

type listEntry struct {
	Folder  bool
	Name    string // file or folder name
	Title   string // document title (empty for folders)
	URL     string
	ModTime time.Time
	Size    int64
	Count   int // documents inside (folders)
}

type collectionData struct {
	Name     string
	Heading  string
	Context  string
	Path     string
	Crumbs   []crumb
	RootURL  string
	AtRoot   bool
	Tree     []treeNode
	Entries  []listEntry
	DocCount int
	Err      string
}

type documentData struct {
	Name      string
	Crumbs    []crumb
	Doc       library.Doc
	AbsPath   string
	EditorURL template.URL
	Rendered  render.Document
	Source    string
	Context   string
}

// handleCollection serves /c/{name}/{path...}: a folder listing, a rendered
// markdown document, or a raw file (images and attachments).
func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.configs.Load()
	if err != nil {
		s.notFound(w, r)
		return
	}
	col := cfg.Collections[r.PathValue("name")]
	if col == nil {
		s.notFound(w, r)
		return
	}
	rel := strings.Trim(r.PathValue("path"), "/")
	if hasHiddenSegment(rel) {
		s.notFound(w, r)
		return
	}
	if rel == "" {
		s.renderFolder(w, r, col, "")
		return
	}
	info, err := library.Stat(col, rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, library.ErrOutsideRoot) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	if info.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, library.WebPath(col.Name, rel, true), http.StatusMovedPermanently)
			return
		}
		s.renderFolder(w, r, col, rel)
		return
	}
	if isMarkdown(rel) {
		s.renderDocument(w, r, cfg, col, rel, info)
		return
	}
	s.serveRaw(w, r, col, rel, info)
}

func (s *Server) renderFolder(w http.ResponseWriter, r *http.Request, col *qmdconfig.Collection, rel string) {
	data := collectionData{
		Name:    col.Name,
		Heading: col.Name,
		Context: col.ContextFor(rel),
		Path:    col.Path,
		Crumbs:  crumbs(col.Name, rel, ""),
		RootURL: library.WebPath(col.Name, "", true),
		AtRoot:  rel == "",
	}
	docs, err := s.docs.Docs(col)
	if err != nil {
		data.Err = friendlyWalkError(err)
	}
	root := library.BuildTree(docs)
	data.DocCount = root.DocCount
	data.Tree = buildTree(col.Name, root, rel)

	if folder := root.Find(rel); folder != nil {
		for _, f := range folder.Folders {
			data.Entries = append(data.Entries, listEntry{
				Folder: true, Name: f.Name, URL: library.WebPath(col.Name, f.Path, true),
				ModTime: f.ModTime, Count: f.DocCount,
			})
		}
		for _, d := range folder.Docs {
			data.Entries = append(data.Entries, listEntry{
				Name: d.Name(), Title: d.Title, URL: library.WebPath(col.Name, d.Path, false),
				ModTime: d.ModTime, Size: d.Size,
			})
		}
	}

	title := col.Name
	if rel != "" {
		data.Heading = path.Base(rel)
		title = data.Heading + " · " + col.Name
	}
	s.render(w, r, http.StatusOK, "collection", s.newPage(r, title, "library", data))
}

// buildTree converts the folder tree for the sidebar, opening the folders on
// the way to the current one.
func buildTree(collection string, f *library.Folder, current string) []treeNode {
	nodes := make([]treeNode, 0, len(f.Folders))
	for _, sub := range f.Folders {
		nodes = append(nodes, treeNode{
			Name:     sub.Name,
			URL:      library.WebPath(collection, sub.Path, true),
			Count:    sub.DocCount,
			Open:     current == sub.Path || strings.HasPrefix(current, sub.Path+"/"),
			Current:  current == sub.Path,
			Children: buildTree(collection, sub, current),
		})
	}
	return nodes
}

func (s *Server) renderDocument(w http.ResponseWriter, r *http.Request, cfg *qmdconfig.Config, col *qmdconfig.Collection, rel string, info fs.FileInfo) {
	src, err := library.ReadFile(col, rel)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d := library.Doc{
		Collection: col.Name,
		Path:       rel,
		Title:      library.Title(src, rel),
		ModTime:    info.ModTime(),
		Size:       info.Size(),
	}
	doc, err := s.renderer.Render(src, render.Location{Collection: col.Name, Path: rel, Title: d.Title})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// The page heading already shows the title, so drop a matching "title" chip.
	meta := doc.Meta[:0:0]
	for _, m := range doc.Meta {
		if !(m.Key == "title" && len(m.Values) == 1 && m.Values[0] == d.Title) {
			meta = append(meta, m)
		}
	}
	doc.Meta = meta
	abs, _ := library.SafeJoin(col.Path, rel)
	data := documentData{
		Name:      col.Name,
		Crumbs:    crumbs(col.Name, path.Dir(rel), d.Title),
		Doc:       d,
		AbsPath:   abs,
		EditorURL: editorURL(s.editorTemplate(cfg), abs),
		Rendered:  doc,
		Source:    string(src),
		Context:   col.ContextFor(rel),
	}
	s.render(w, r, http.StatusOK, "document", s.newPage(r, d.Title, "library", data))
}

// serveRaw serves a non-markdown file (image, PDF…) from the collection.
// The sandbox CSP stops HTML or SVG files from running scripts on lantern's origin.
func (s *Server) serveRaw(w http.ResponseWriter, r *http.Request, col *qmdconfig.Collection, rel string, info fs.FileInfo) {
	p, err := library.SafeJoin(col.Path, rel)
	if err != nil {
		s.notFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		s.notFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// crumbs builds Library / collection / folder… / [last].
func crumbs(collection, dir, last string) []crumb {
	dir = strings.Trim(dir, "/")
	if dir == "." {
		dir = ""
	}
	out := []crumb{{Name: "Library", URL: "/"}}
	if dir == "" && last == "" {
		return append(out, crumb{Name: collection})
	}
	out = append(out, crumb{Name: collection, URL: library.WebPath(collection, "", true)})
	if dir != "" {
		segs := strings.Split(dir, "/")
		for i, seg := range segs {
			c := crumb{Name: seg}
			if i < len(segs)-1 || last != "" {
				c.URL = library.WebPath(collection, strings.Join(segs[:i+1], "/"), true)
			}
			out = append(out, c)
		}
	}
	if last != "" {
		out = append(out, crumb{Name: last})
	}
	return out
}

func (s *Server) editorTemplate(cfg *qmdconfig.Config) string {
	if s.opts.EditorURI != "" {
		return s.opts.EditorURI
	}
	if cfg != nil && cfg.EditorURI != "" {
		return cfg.EditorURI
	}
	return defaultEditorURI
}

// editorURL fills an editor URI template ({path}, {line}, {col}). Templates
// with a script-capable scheme are refused.
func editorURL(tmpl, abs string) template.URL {
	p := (&url.URL{Path: abs}).EscapedPath()
	out := strings.NewReplacer("{path}", p, "{line}", "1", "{col}", "1", "{column}", "1").Replace(tmpl)
	scheme, _, ok := strings.Cut(out, ":")
	if !ok {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "javascript", "data", "vbscript", "":
		return ""
	}
	return template.URL(out) // trusted: comes from the user's own config or env
}

func isMarkdown(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

// hasHiddenSegment reports whether any path segment starts with "." (".git", ".env"…).
func hasHiddenSegment(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}
