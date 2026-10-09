package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"time"
)

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"asset":  s.assetURL,
		"uptime": formatUptime,
	}
}

// assetURL is the static URL with a content hash, so browsers refetch after a rebuild.
func (s *Server) assetURL(name string) string {
	if h, ok := s.assets[name]; ok {
		return "/static/" + name + "?v=" + h
	}
	return "/static/" + name
}

func hashAssets(static fs.FS) (map[string]string, error) {
	out := map[string]string{}
	err := fs.WalkDir(static, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(static, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[p] = hex.EncodeToString(sum[:4])
		return nil
	})
	return out, err
}

// formatUptime renders a duration as "45s", "12m", "3h 5m" or "2d 4h".
func formatUptime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
