package config

import (
	"io"
	"testing"
)

func env(m map[string]string) Env { return func(k string) string { return m[k] } }

func TestParseDefaults(t *testing.T) {
	c, err := Parse(nil, env(nil), "/home/me", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != DefaultAddr || c.QMDURL != DefaultQMDURL || c.Open || c.QMDIndex != "" {
		t.Errorf("defaults = %+v", c)
	}
	if c.QMDConfigPath != "/home/me/.config/qmd/index.yml" {
		t.Errorf("QMDConfigPath = %q", c.QMDConfigPath)
	}
	if c.URL() != "http://127.0.0.1:8090" || !c.Loopback() {
		t.Errorf("URL = %q, Loopback = %v", c.URL(), c.Loopback())
	}
}

func TestParseEnvAndFlags(t *testing.T) {
	e := env(map[string]string{
		"LANTERN_ADDR":       "127.0.0.1:9000",
		"LANTERN_QMD_URL":    "http://localhost:9999/",
		"LANTERN_QMD_INDEX":  "work",
		"LANTERN_QMD_CONFIG": "",
		"QMD_CONFIG_DIR":     "/cfg",
	})
	c, err := Parse(nil, e, "/home/me", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != "127.0.0.1:9000" || c.QMDURL != "http://localhost:9999" || c.QMDConfigPath != "/cfg/work.yml" {
		t.Errorf("from env = %+v", c)
	}

	c, err = Parse([]string{"--addr", ":7000", "--qmd-config", "/x/index.yml", "--open"}, e, "/home/me", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":7000" || c.QMDConfigPath != "/x/index.yml" || !c.Open {
		t.Errorf("flags should beat env: %+v", c)
	}
	if c.URL() != "http://127.0.0.1:7000" || c.Loopback() {
		t.Errorf("URL = %q, Loopback = %v", c.URL(), c.Loopback())
	}
}

func TestParseErrors(t *testing.T) {
	tests := [][]string{
		{"--addr", "nonsense"},
		{"--qmd-url", "localhost:8181"},
		{"--qmd-url", "ftp://x"},
		{"--qmd-index", "../evil"},
		{"--unknown"},
		{"extra-arg"},
	}
	for _, args := range tests {
		if _, err := Parse(args, env(nil), "/h", io.Discard); err == nil {
			t.Errorf("Parse(%v) should fail", args)
		}
	}
}

func TestLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:1": true, "localhost:1": true, "[::1]:1": true,
		"0.0.0.0:1": false, ":1": false, "192.168.1.5:1": false,
	} {
		if got := (Config{Addr: addr}).Loopback(); got != want {
			t.Errorf("Loopback(%q) = %v", addr, got)
		}
	}
}
