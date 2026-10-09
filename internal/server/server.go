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

	"github.com/4rji/lantern/internal/qmdconfig"
	"github.com/4rji/lantern/web"
)

// Options configures a Server.
type Options struct {
	Version       string
	QMDURL        string
	QMDConfigPath string
	// LocalOnly rejects requests whose Host is not a loopback name. It blocks
	// DNS-rebinding pages from reading notes through a server bound to 127.0.0.1.
	LocalOnly bool
}

// Server serves the lantern dashboard.
type Server struct {
	opts    Options
	status  *statusCache
	configs *configStore
	pages   map[string]*template.Template
	static  fs.FS
	assets  map[string]string // static file name -> content hash
	handler http.Handler
}

// pageNames are the templates under web/templates/pages.
var pageNames = []string{"library", "search", "ask", "settings", "error"}

// New builds a Server. health is usually a *qmd.Client.
func New(opts Options, health healthChecker) (*Server, error) {
	s := &Server{
		opts:    opts,
		status:  newStatusCache(health),
		configs: newConfigStore(opts.QMDConfigPath),
		pages:   map[string]*template.Template{},
	}
	static, err := fs.Sub(web.FS, "static")
	if err != nil {
		return nil, err
	}
	s.static = static
	if s.assets, err = hashAssets(static); err != nil {
		return nil, err
	}
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
	mux.HandleFunc("GET /{$}", s.page("library", "Library"))
	mux.HandleFunc("GET /search", s.page("search", "Search"))
	mux.HandleFunc("GET /ask", s.page("ask", "Ask AI"))
	mux.HandleFunc("GET /settings", s.page("settings", "Settings"))
	mux.HandleFunc("GET /partials/status", s.handleStatus)
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

// page returns a handler that renders a page with no extra data.
func (s *Server) page(name, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.render(w, r, http.StatusOK, name, s.newPage(r, title, name, nil))
	}
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
