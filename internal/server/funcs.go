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
		"asset":    s.assetURL,
		"uptime":   formatUptime,
		"date":     formatDate,
		"datetime": func(t time.Time) string { return t.Format("Jan 2, 2006 15:04") },
		"isodate":  func(t time.Time) string { return t.Format(time.RFC3339) },
		"bytes":    formatBytes,
		"plural":   plural,
		"seconds":  formatSeconds,
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
		out[p] = contentHash(b)
		return nil
	})
	return out, err
}

func contentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:4])
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

// formatDate is "Today", "Yesterday", "Mar 4" (this year) or "Mar 4, 2024".
func formatDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	now := time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "Today " + t.Format("15:04")
	case now.Sub(t) < 48*time.Hour && now.AddDate(0, 0, -1).Day() == d1:
		return "Yesterday " + t.Format("15:04")
	case y1 == y2:
		return t.Format("Jan 2")
	default:
		return t.Format("Jan 2, 2006")
	}
}

func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// plural renders "1 document" / "3 documents".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// formatSeconds renders a short elapsed time: "45 ms", "1.2 s", "75 s".
func formatSeconds(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < 10*time.Second:
		return fmt.Sprintf("%.1f s", d.Seconds())
	default:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
}
