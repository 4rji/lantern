// Package config builds the app configuration from flags and environment variables.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/4rji/lantern/internal/qmdconfig"
)

// Defaults.
const (
	DefaultAddr   = "127.0.0.1:8090"
	DefaultQMDURL = "http://localhost:8181"
)

// Config is lantern's runtime configuration.
type Config struct {
	Addr      string // listen address
	QMDURL    string // base URL of the QMD HTTP server
	QMDConfig string // explicit index.yml path from --qmd-config (may be empty)
	QMDIndex  string // named QMD index (empty = default)
	Open      bool   // open the browser on start
	Version   bool   // print the version and exit

	// QMDConfigPath is the resolved index.yml path.
	QMDConfigPath string
}

// Env abstracts environment lookups so parsing is testable.
type Env func(key string) string

// Parse reads flags from args (without the program name). Each flag falls back
// to its LANTERN_* environment variable, then to the default.
func Parse(args []string, env Env, home string, stderr io.Writer) (Config, error) {
	var c Config
	fs := flag.NewFlagSet("lantern", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.Addr, "addr", envOr(env, "LANTERN_ADDR", DefaultAddr), "listen address (env LANTERN_ADDR)")
	fs.StringVar(&c.QMDURL, "qmd-url", envOr(env, "LANTERN_QMD_URL", DefaultQMDURL), "QMD HTTP server URL (env LANTERN_QMD_URL)")
	fs.StringVar(&c.QMDConfig, "qmd-config", env("LANTERN_QMD_CONFIG"), "path to QMD's index.yml (env LANTERN_QMD_CONFIG; default: resolved like QMD)")
	fs.StringVar(&c.QMDIndex, "qmd-index", env("LANTERN_QMD_INDEX"), "named QMD index, reads <name>.yml (env LANTERN_QMD_INDEX)")
	fs.BoolVar(&c.Open, "open", false, "open the dashboard in the browser on start")
	fs.BoolVar(&c.Version, "version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	if _, _, err := net.SplitHostPort(c.Addr); err != nil {
		return Config{}, fmt.Errorf("invalid --addr %q: %w", c.Addr, err)
	}
	u, err := url.Parse(c.QMDURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("invalid --qmd-url %q: want something like %s", c.QMDURL, DefaultQMDURL)
	}
	c.QMDURL = strings.TrimRight(c.QMDURL, "/")
	if strings.ContainsAny(c.QMDIndex, `/\`) {
		return Config{}, errors.New("--qmd-index must be a name, not a path")
	}
	c.QMDConfigPath = qmdconfig.Locate(c.QMDConfig, c.QMDIndex, qmdconfig.Env(env), home)
	return c, nil
}

// URL is the dashboard address to show and open.
func (c Config) URL() string {
	host, port, _ := net.SplitHostPort(c.Addr)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// Loopback reports whether the listen address only accepts local connections.
func (c Config) Loopback() bool {
	host, _, _ := net.SplitHostPort(c.Addr)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envOr(env Env, key, def string) string {
	if v := env(key); v != "" {
		return v
	}
	return def
}
