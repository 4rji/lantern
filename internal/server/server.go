// Package server holds the HTTP routes, handlers and template rendering.
package server

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/4rji/lantern/internal/ai"
	"github.com/4rji/lantern/internal/library"
	"github.com/4rji/lantern/internal/qmdconfig"
	"github.com/4rji/lantern/internal/render"
	"github.com/4rji/lantern/web"
)

// Options configures a Server.
type Options struct {
	Version       string
	QMDURL        string
	QMDConfigPath string
	// Providers are the AI providers shown on Ask AI and Settings.
	Providers []ai.Provider
	// EditorURI overrides the "Open in editor" template, like QMD's QMD_EDITOR_URI.
	EditorURI string
	// LocalOnly rejects requests whose Host is not a loopback name. It blocks
	// DNS-rebinding pages from reading notes through a server bound to 127.0.0.1.
	LocalOnly bool
}

// Server serves the lantern dashboard.
type Server struct {
	opts     Options
	status   *statusCache
	configs  *configStore
	pages    map[string]*template.Template
	static   fs.FS
	assets   map[string]string // static file name -> content hash
	codeCSS  []byte            // generated syntax-highlighting CSS
	docs     *docCache
	renderer *render.Renderer
	query    querier
	handler  http.Handler
}

// pageNames are the templates under web/templates/pages.
var pageNames = []string{"library", "collection", "document", "search", "ask", "settings", "error"}

// QMD is what the server needs from the QMD client.
type QMD interface {
	healthChecker
	querier
}

// New builds a Server. client is usually a *qmd.Client.
func New(opts Options, client QMD) (*Server, error) {
	s := &Server{
		opts:     opts,
		status:   newStatusCache(client),
		query:    client,
		configs:  newConfigStore(opts.QMDConfigPath),
		pages:    map[string]*template.Template{},
		docs:     newDocCache(),
		renderer: render.New(),
	}
	css, err := render.CodeCSS()
	if err != nil {
		return nil, fmt.Errorf("build code CSS: %w", err)
	}
	s.codeCSS = []byte(css)
	static, err := fs.Sub(web.FS, "static")
	if err != nil {
		return nil, err
	}
	s.static = static
	if s.assets, err = hashAssets(static); err != nil {
		return nil, err
	}
	s.assets["code.css"] = contentHash(s.codeCSS)
	for _, name := range pageNames {
		t, err := template.New(name).Funcs(s.funcs()).ParseFS(web.FS,
			"templates/layout.html",
			"templates/partials/*.html",
			"templates/pages/"+name+".html",
		)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		s.pages[name] = t
	}
	s.handler = s.routes()
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleLibrary)
	mux.HandleFunc("GET /c/{name}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, library.WebPath(r.PathValue("name"), "", true), http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /c/{name}/{path...}", s.handleCollection)
	mux.HandleFunc("GET /search", s.handleSearchPage)
	mux.HandleFunc("GET /search/results", s.handleSearchResults)
	mux.HandleFunc("GET /ask", s.handleAskPage)
	mux.HandleFunc("POST /api/ask", s.handleAPIAsk)
	mux.HandleFunc("/api/ask", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "use POST")
	})
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("GET /partials/status", s.handleStatus)
	mux.HandleFunc("GET /static/code.css", s.handleCodeCSS)
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticCache(http.FileServerFS(s.static))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.notFound(w, r)
	})
	return s.recoverer(s.hostGuard(securityHeaders(mux)))
}

// pageData is what every full page template receives.
type pageData struct {
	Title      string
	Nav        string // active nav item
	Status     Status
	Config     *qmdconfig.Config
	ConfigPath string
	ConfigErr  string
	QMDURL     string
	Version    string
	Data       any
}

func (s *Server) newPage(r *http.Request, title, nav string, data any) pageData {
	p := pageData{
		Title:      title,
		Nav:        nav,
		Status:     s.status.Get(r.Context()),
		ConfigPath: s.configs.Path(),
		QMDURL:     s.opts.QMDURL,
		Version:    s.opts.Version,
		Data:       data,
	}
	cfg, err := s.configs.Load()
	if err != nil {
		p.ConfigErr = err.Error()
	} else {
		p.Config = cfg
	}
	return p
}

// render executes a page template into a buffer first, so a template error
// becomes a clean 500 instead of a half-written page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, name string, data pageData) {
	t, ok := s.pages[name]
	if !ok {
		s.serverError(w, r, fmt.Errorf("unknown page %q", name))
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		if name == "error" {
			log.Printf("render error page: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	buf.WriteTo(w)
}

// renderPartial executes a named template (no layout) from the given page set.
func (s *Server) renderPartial(w http.ResponseWriter, r *http.Request, page, tmpl string, data any) {
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, tmpl, data); err != nil {
		log.Printf("render partial %s: %v", tmpl, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

type errorData struct {
	Code    int
	Heading string
	Message string
	Detail  string
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "error", s.newPage(r, "Not found", "", errorData{
		Code:    http.StatusNotFound,
		Heading: "Page not found",
		Message: "There is nothing at " + r.URL.Path + ".",
	}))
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	s.render(w, r, http.StatusInternalServerError, "error", s.newPage(r, "Error", "", errorData{
		Code:    http.StatusInternalServerError,
		Heading: "Something went wrong",
		Message: "lantern hit an error while building this page.",
		Detail:  err.Error(),
	}))
}

// handleStatus returns the status pill and, out of band, the QMD banner.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.renderPartial(w, r, "library", "status-update", s.newPage(r, "", "", nil))
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.serverError(w, r, fmt.Errorf("panic: %v", v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostGuard(next http.Handler) http.Handler {
	if !s.opts.LocalOnly {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(strings.ToLower(host), "[]")
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "lantern only answers to localhost", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func staticCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleCodeCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(s.codeCSS)
}
